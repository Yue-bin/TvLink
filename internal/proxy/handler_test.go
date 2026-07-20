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
	p.UpdateUsage("one", pool.Usage{Limit: 1000}, time.Now())
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
	p.UpdateUsage("one", pool.Usage{Limit: 1000}, time.Now())
	handler := New("tlk-client", upstream.URL, upstream.Client(), p, 1024, time.Hour)
	create := httptest.NewRequest(http.MethodPost, "/research", bytes.NewBufferString(`{"input":"test"}`))
	create.Header.Set("Authorization", "Bearer tlk-client")
	handler.ServeHTTP(httptest.NewRecorder(), create)
	if snapshot := p.Snapshots(time.Now())[0]; snapshot.ResearchReserved != 250 {
		t.Fatalf("ResearchReserved after creation = %v, want 250", snapshot.ResearchReserved)
	}

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
	p.UpdateUsage("one", pool.Usage{Limit: 1000}, time.Now())
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
	p.UpdateUsage("one", pool.Usage{Limit: 1000, Used: 25}, time.Now())
	if snapshot := p.Snapshots(time.Now())[0]; snapshot.EstimatedUsage != 0 || snapshot.ResearchReserved != 0 {
		t.Fatalf("snapshot after expiry refresh = %#v", snapshot)
	}
}

func TestEstimateUsesResearchModelDefaults(t *testing.T) {
	tests := []struct {
		name string
		body string
		want float64
	}{
		{name: "mini", body: `{"model":"mini"}`, want: 110},
		{name: "pro", body: `{"model":"pro"}`, want: 250},
		{name: "auto", body: `{"model":"auto"}`, want: 250},
		{name: "omitted", body: `{}`, want: 250},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := estimate("/research", []byte(test.body)); got != test.want {
				t.Fatalf("estimate() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestResearchModelForLoggingUsesWhitelist(t *testing.T) {
	tests := []struct {
		body string
		want string
	}{
		{body: `{"model":"mini"}`, want: "mini"},
		{body: `{"model":"pro"}`, want: "pro"},
		{body: `{"model":"auto"}`, want: "auto"},
		{body: `{"model":"private-input"}`, want: "auto"},
		{body: `{`, want: "auto"},
	}
	for _, test := range tests {
		if got := researchModel([]byte(test.body)); got != test.want {
			t.Errorf("researchModel(%q) = %q, want %q", test.body, got, test.want)
		}
	}
}

func TestRESTResearchRetriesQuotaErrorWithAnotherKey(t *testing.T) {
	var authorizations []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		if len(authorizations) == 1 {
			w.WriteHeader(432)
			_, _ = w.Write([]byte(`{"detail":{"error":"plan limit"}}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"request_id":"research-1","status":"pending"}`))
	}))
	defer upstream.Close()

	handler, keyPool := newTwoKeyResearchHandler(upstream.URL, upstream.Client(), time.Hour)
	request := httptest.NewRequest(http.MethodPost, "/research", bytes.NewBufferString(`{"input":"test","model":"mini"}`))
	request.Header.Set("Authorization", "Bearer tlk-client")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", response.Code, response.Body.String())
	}
	if len(authorizations) != 2 || authorizations[0] == authorizations[1] {
		t.Fatalf("authorizations = %v, want two distinct Keys", authorizations)
	}
	for _, snapshot := range keyPool.Snapshots(time.Now()) {
		if snapshot.RealUsage != 0 || snapshot.State != pool.StateReady {
			t.Fatalf("snapshot = %#v", snapshot)
		}
	}
}

func TestRESTResearchReturnsLastQuotaResponseAfterTryingEveryKey(t *testing.T) {
	var authorizations []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		if len(authorizations) == 1 {
			w.WriteHeader(432)
			_, _ = w.Write([]byte(`{"detail":{"error":"plan limit"}}`))
			return
		}
		w.WriteHeader(433)
		_, _ = w.Write([]byte(`{"detail":{"error":"paygo limit"}}`))
	}))
	defer upstream.Close()

	handler, _ := newTwoKeyResearchHandler(upstream.URL, upstream.Client(), time.Hour)
	request := httptest.NewRequest(http.MethodPost, "/research", bytes.NewBufferString(`{"input":"test","model":"mini"}`))
	request.Header.Set("Authorization", "Bearer tlk-client")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != 433 || !bytes.Contains(response.Body.Bytes(), []byte("paygo limit")) {
		t.Fatalf("response = %d %s, want final 433", response.Code, response.Body.String())
	}
	if len(authorizations) != 2 || authorizations[0] == authorizations[1] {
		t.Fatalf("authorizations = %v, want each Key once", authorizations)
	}
}

func newTwoKeyResearchHandler(upstreamURL string, client *http.Client, ttl time.Duration) (*Handler, *pool.Pool) {
	keys := []pool.Key{{Name: "one", APIKey: "tvly-one"}, {Name: "two", APIKey: "tvly-two"}}
	keyPool := pool.New(keys, 1)
	for _, key := range keys {
		keyPool.UpdateUsage(key.Name, pool.Usage{Limit: 1000}, time.Now())
	}
	return New("tlk-client", upstreamURL, client, keyPool, 1024, ttl), keyPool
}
