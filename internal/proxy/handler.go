// Package proxy exposes authenticated Tavily-compatible HTTP proxy routes.
package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"tvlink/internal/pool"
)

// Handler authenticates callers and forwards supported Tavily requests.
type Handler struct {
	clientKey  string
	baseURL    string
	http       *http.Client
	pool       *pool.Pool
	maxBody    int64
	researchMu sync.Mutex
	research   map[string]string
}

// New creates a proxy handler.
func New(clientKey, upstreamBaseURL string, httpClient *http.Client, keyPool *pool.Pool, maxBody int64) *Handler {
	return &Handler{
		clientKey: clientKey,
		baseURL:   strings.TrimRight(upstreamBaseURL, "/"),
		http:      httpClient,
		pool:      keyPool,
		maxBody:   maxBody,
		research:  make(map[string]string),
	}
}

// ServeHTTP proxies Tavily REST requests.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+h.clientKey {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/research/") {
		h.serveResearchStatus(w, r)
		return
	}
	if r.Method != http.MethodPost || !supportedPostPath(r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, h.maxBody+1))
	if err != nil {
		http.Error(w, "read request body", http.StatusBadRequest)
		return
	}
	if int64(len(body)) > h.maxBody {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}

	for attempt := 0; attempt < 2; attempt++ {
		lease, err := h.pool.Select(time.Now(), estimate(r.URL.Path, body))
		if err != nil {
			http.Error(w, "no Tavily key is currently available", http.StatusServiceUnavailable)
			return
		}
		upstream, err := http.NewRequestWithContext(r.Context(), r.Method, h.baseURL+r.URL.Path, bytes.NewReader(body))
		if err != nil {
			http.Error(w, "build upstream request", http.StatusBadGateway)
			return
		}
		copyHeader(upstream.Header, r.Header)
		upstream.Header.Set("Authorization", "Bearer "+lease.Key.APIKey)
		response, err := h.http.Do(upstream)
		if err != nil {
			h.pool.Resolve(lease, http.StatusInternalServerError, 0, time.Now())
			http.Error(w, "upstream request failed", http.StatusBadGateway)
			return
		}
		if response.StatusCode == http.StatusTooManyRequests && attempt == 0 && r.URL.Path != "/research" {
			h.pool.Resolve(lease, response.StatusCode, retryAfter(response.Header.Get("Retry-After")), time.Now())
			response.Body.Close()
			continue
		}
		h.pool.Resolve(lease, response.StatusCode, retryAfter(response.Header.Get("Retry-After")), time.Now())
		defer response.Body.Close()
		if r.URL.Path == "/research" && !strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
			payload, readErr := io.ReadAll(response.Body)
			if readErr != nil {
				http.Error(w, "read research response", http.StatusBadGateway)
				return
			}
			var result struct {
				RequestID string `json:"request_id"`
			}
			if json.Unmarshal(payload, &result) == nil && result.RequestID != "" {
				h.researchMu.Lock()
				h.research[result.RequestID] = lease.Key.Name
				h.researchMu.Unlock()
			}
			copyHeader(w.Header(), response.Header)
			w.WriteHeader(response.StatusCode)
			_, _ = w.Write(payload)
			return
		}
		copyHeader(w.Header(), response.Header)
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
		return
	}
	http.Error(w, "no Tavily key is currently available", http.StatusServiceUnavailable)
}

func (h *Handler) serveResearchStatus(w http.ResponseWriter, r *http.Request) {
	requestID := strings.TrimPrefix(r.URL.Path, "/research/")
	h.researchMu.Lock()
	keyName, ok := h.research[requestID]
	h.researchMu.Unlock()
	if !ok {
		http.Error(w, "research request not found", http.StatusNotFound)
		return
	}
	key, ok := h.pool.Key(keyName)
	if !ok {
		http.Error(w, "research key unavailable", http.StatusServiceUnavailable)
		return
	}
	upstream, err := http.NewRequestWithContext(r.Context(), http.MethodGet, h.baseURL+r.URL.Path, nil)
	if err != nil {
		http.Error(w, "build upstream request", http.StatusBadGateway)
		return
	}
	copyHeader(upstream.Header, r.Header)
	upstream.Header.Set("Authorization", "Bearer "+key.APIKey)
	response, err := h.http.Do(upstream)
	if err != nil {
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	copyHeader(w.Header(), response.Header)
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}

func supportedPostPath(path string) bool {
	switch path {
	case "/search", "/extract", "/crawl", "/map", "/research":
		return true
	default:
		return false
	}
}

func estimate(path string, body []byte) float64 {
	if path == "/search" && bytes.Contains(body, []byte(`"search_depth":"advanced"`)) {
		return 2
	}
	return 1
}

func copyHeader(destination, source http.Header) {
	for _, name := range []string{"Accept", "Content-Type", "X-Project-ID", "X-Session-Id", "X-Human-Id"} {
		if value := source.Get(name); value != "" {
			destination.Set(name, value)
		}
	}
}

func retryAfter(value string) time.Duration {
	seconds, err := time.ParseDuration(value + "s")
	if err == nil && seconds > 0 {
		return seconds
	}
	return time.Minute
}
