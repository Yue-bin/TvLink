package proxy

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tvlink/internal/pool"
)

func TestHandlerRejectsInvalidClientKey(t *testing.T) {
	handler := New("tlk-client", "http://example.invalid", http.DefaultClient, pool.New(nil, 1), 1024, time.Hour)
	request := httptest.NewRequest(http.MethodPost, "/search", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestHandlerReplacesUpstreamAuthorization(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tvly-one" {
			t.Errorf("upstream authorization = %q", got)
		}
		if got := r.Header.Get("X-Project-ID"); got != "project-1" {
			t.Errorf("X-Project-ID = %q", got)
		}
		_, _ = w.Write([]byte(`{"answer":"ok"}`))
	}))
	defer upstream.Close()

	p := pool.New([]pool.Key{{Name: "one", APIKey: "tvly-one"}}, 1)
	p.UpdateUsage("one", pool.Usage{Limit: 100}, time.Now())
	handler := New("tlk-client", upstream.URL, upstream.Client(), p, 1024, time.Hour)
	request := httptest.NewRequest(http.MethodPost, "/search", bytes.NewBufferString(`{"query":"test"}`))
	request.Header.Set("Authorization", "Bearer tlk-client")
	request.Header.Set("X-Project-ID", "project-1")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", response.Code)
	}
	if response.Body.String() != `{"answer":"ok"}` {
		t.Errorf("body = %q", response.Body.String())
	}
}

func TestResearchStatusUsesCreatingKey(t *testing.T) {
	var authorization string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"request_id":"research-1","status":"pending"}`))
			return
		}
		_, _ = w.Write([]byte(`{"request_id":"research-1","status":"completed"}`))
	}))
	defer upstream.Close()

	p := pool.New([]pool.Key{{Name: "one", APIKey: "tvly-one"}}, 1)
	p.UpdateUsage("one", pool.Usage{Limit: 100}, time.Now())
	handler := New("tlk-client", upstream.URL, upstream.Client(), p, 1024, time.Hour)
	create := httptest.NewRequest(http.MethodPost, "/research", bytes.NewBufferString(`{"input":"test"}`))
	create.Header.Set("Authorization", "Bearer tlk-client")
	handler.ServeHTTP(httptest.NewRecorder(), create)

	status := httptest.NewRequest(http.MethodGet, "/research/research-1", nil)
	status.Header.Set("Authorization", "Bearer tlk-client")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, status)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if authorization != "Bearer tvly-one" {
		t.Errorf("status authorization = %q", authorization)
	}
}

func TestResearchStatusRemovesExpiredMapping(t *testing.T) {
	upstreamCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"request_id":"research-1","status":"pending"}`))
			return
		}
		_, _ = w.Write([]byte(`{"request_id":"research-1","status":"completed"}`))
	}))
	defer upstream.Close()

	p := pool.New([]pool.Key{{Name: "one", APIKey: "tvly-one"}}, 1)
	p.UpdateUsage("one", pool.Usage{Limit: 100}, time.Now())
	handler := New("tlk-client", upstream.URL, upstream.Client(), p, 1024, 10*time.Millisecond)
	create := httptest.NewRequest(http.MethodPost, "/research", bytes.NewBufferString(`{"input":"test"}`))
	create.Header.Set("Authorization", "Bearer tlk-client")
	handler.ServeHTTP(httptest.NewRecorder(), create)

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		handler.researchMu.Lock()
		_, exists := handler.research["research-1"]
		handler.researchMu.Unlock()
		if !exists {
			break
		}
		select {
		case <-deadline:
			t.Fatal("expired research mapping was not removed")
		case <-ticker.C:
		}
	}

	status := httptest.NewRequest(http.MethodGet, "/research/research-1", nil)
	status.Header.Set("Authorization", "Bearer tlk-client")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, status)

	if response.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
	if upstreamCalls != 1 {
		t.Errorf("upstream calls = %d, want 1", upstreamCalls)
	}
}
