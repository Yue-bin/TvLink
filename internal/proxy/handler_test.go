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
	handler := New("tlk-client", "http://example.invalid", http.DefaultClient, pool.New(nil, 1), 1024)
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
	handler := New("tlk-client", upstream.URL, upstream.Client(), p, 1024)
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
