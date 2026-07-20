package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"tvlink/internal/pool"
)

func TestRunResearchPollsWithCreatingKey(t *testing.T) {
	var methods, paths, authorizations []string
	var createBody map[string]any
	polls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		paths = append(paths, r.URL.Path)
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			if err := json.NewDecoder(r.Body).Decode(&createBody); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"request_id":"research-1","created_at":"2026-07-21T00:00:00Z","status":"pending","input":"test","model":"mini","response_time":0.1}`))
			return
		}
		polls++
		if polls == 1 {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"request_id":"research-1","status":"in_progress","response_time":0.2}`))
			return
		}
		_, _ = w.Write([]byte(`{"request_id":"research-1","created_at":"2026-07-21T00:00:00Z","status":"completed","content":"report","sources":[],"response_time":1.2}`))
	}))
	defer upstream.Close()

	handler := newResearchTestHandler(upstream.URL, upstream.Client())
	handler.researchPollInterval = time.Millisecond
	var statuses []string
	result, err := handler.RunResearch(context.Background(), []byte(`{"input":"test","model":"mini","stream":true}`), func(status string) {
		statuses = append(statuses, status)
	})
	if err != nil {
		t.Fatalf("RunResearch() error = %v", err)
	}
	if !bytes.Contains(result, []byte(`"status":"completed"`)) {
		t.Fatalf("result = %s", result)
	}
	if createBody["stream"] != false {
		t.Errorf("create stream = %#v, want false", createBody["stream"])
	}
	if !reflect.DeepEqual(methods, []string{http.MethodPost, http.MethodGet, http.MethodGet}) {
		t.Errorf("methods = %v", methods)
	}
	if !reflect.DeepEqual(paths, []string{"/research", "/research/research-1", "/research/research-1"}) {
		t.Errorf("paths = %v", paths)
	}
	if !reflect.DeepEqual(authorizations, []string{"Bearer tvly-one", "Bearer tvly-one", "Bearer tvly-one"}) {
		t.Errorf("authorizations = %v", authorizations)
	}
	if !reflect.DeepEqual(statuses, []string{"pending", "in_progress"}) {
		t.Errorf("statuses = %v", statuses)
	}
}

func TestRunResearchRejectsInvalidTerminalResponses(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantError  string
	}{
		{name: "missing request id", statusCode: http.StatusCreated, body: `{"status":"pending"}`, wantError: "missing request_id"},
		{name: "failed", statusCode: http.StatusCreated, body: `{"request_id":"research-1","status":"failed"}`, wantError: "research task failed"},
		{name: "malformed", statusCode: http.StatusCreated, body: `{`, wantError: "decode research response"},
		{name: "non-success", statusCode: http.StatusBadGateway, body: `bad gateway`, wantError: "research request returned 502"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.statusCode)
				_, _ = w.Write([]byte(test.body))
			}))
			defer upstream.Close()

			handler := newResearchTestHandler(upstream.URL, upstream.Client())
			_, err := handler.RunResearch(context.Background(), []byte(`{"input":"test"}`), nil)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("RunResearch() error = %v, want containing %q", err, test.wantError)
			}
		})
	}
}

func TestRunResearchHonorsCancellation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"request_id":"research-1","status":"pending"}`))
	}))
	defer upstream.Close()

	handler := newResearchTestHandler(upstream.URL, upstream.Client())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := handler.RunResearch(ctx, []byte(`{"input":"test"}`), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunResearch() error = %v, want context canceled", err)
	}
}

func newResearchTestHandler(upstreamURL string, client *http.Client) *Handler {
	keyPool := pool.New([]pool.Key{{Name: "one", APIKey: "tvly-one"}}, 1)
	keyPool.UpdateUsage("one", pool.Usage{Limit: 100}, time.Now())
	return New("tlk-client", upstreamURL, client, keyPool, 1024, time.Hour)
}
