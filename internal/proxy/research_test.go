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

func TestRunResearchRetriesQuotaErrorWithAnotherKey(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"request_id":"research-1","status":"completed","content":"ok"}`))
	}))
	defer upstream.Close()

	handler, keyPool := newTwoKeyResearchHandler(upstream.URL, upstream.Client(), time.Hour)
	result, err := handler.RunResearch(context.Background(), []byte(`{"input":"test","model":"mini"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(result, []byte(`"content":"ok"`)) {
		t.Fatalf("result = %s", result)
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

func TestRunResearchReturnsLastQuotaErrorAfterTryingEveryKey(t *testing.T) {
	var authorizations []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorizations = append(authorizations, r.Header.Get("Authorization"))
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
	_, err := handler.RunResearch(context.Background(), []byte(`{"input":"test","model":"mini"}`), nil)
	if err == nil || !strings.Contains(err.Error(), "433") || !strings.Contains(err.Error(), "paygo limit") {
		t.Fatalf("RunResearch() error = %v, want final 433 response", err)
	}
	if len(authorizations) != 2 || authorizations[0] == authorizations[1] {
		t.Fatalf("authorizations = %v, want each Key once", authorizations)
	}
}

func TestRunResearchReconcilesReservationAtTerminalStatus(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"request_id":"research-1","status":"completed","content":"ok"}`))
	}))
	defer upstream.Close()

	keyPool := pool.New([]pool.Key{{Name: "one", APIKey: "tvly-one"}}, 1)
	keyPool.UpdateUsage("one", pool.Usage{Limit: 1000}, time.Now())
	handler := New("tlk-client", upstream.URL, upstream.Client(), keyPool, 1024, time.Hour)
	refresher := &fakeUsageRefresher{pool: keyPool, used: 73}
	handler.usage = refresher

	if _, err := handler.RunResearch(context.Background(), []byte(`{"input":"test","model":"mini"}`), nil); err != nil {
		t.Fatal(err)
	}
	snapshot := keyPool.Snapshots(time.Now())[0]
	if snapshot.RealUsage != 73 || snapshot.EstimatedUsage != 0 || snapshot.ResearchReserved != 0 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if !reflect.DeepEqual(refresher.calls, []string{"one"}) {
		t.Fatalf("refresh calls = %v", refresher.calls)
	}
}

func TestRunResearchRetainsReservationWhenTerminalRefreshFails(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"request_id":"research-1","status":"completed","content":"ok"}`))
	}))
	defer upstream.Close()

	keyPool := pool.New([]pool.Key{{Name: "one", APIKey: "tvly-one"}}, 1)
	keyPool.UpdateUsage("one", pool.Usage{Limit: 1000}, time.Now())
	handler := New("tlk-client", upstream.URL, upstream.Client(), keyPool, 1024, time.Hour)
	handler.usage = &fakeUsageRefresher{pool: keyPool, err: errors.New("usage unavailable")}

	if _, err := handler.RunResearch(context.Background(), []byte(`{"input":"test","model":"mini"}`), nil); err != nil {
		t.Fatal(err)
	}
	snapshot := keyPool.Snapshots(time.Now())[0]
	if snapshot.RealUsage != 0 || snapshot.EstimatedUsage != 110 {
		t.Fatalf("snapshot = %#v, want conservative settled reservation", snapshot)
	}
}

func TestRunResearchCancellationKeepsActiveReservation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"request_id":"research-1","status":"pending"}`))
	}))
	defer upstream.Close()

	keyPool := pool.New([]pool.Key{{Name: "one", APIKey: "tvly-one"}}, 1)
	keyPool.UpdateUsage("one", pool.Usage{Limit: 1000}, time.Now())
	handler := New("tlk-client", upstream.URL, upstream.Client(), keyPool, 1024, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	_, err := handler.RunResearch(ctx, []byte(`{"input":"test","model":"mini"}`), func(string) { cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunResearch() error = %v, want context canceled", err)
	}
	snapshot := keyPool.Snapshots(time.Now())[0]
	if snapshot.EstimatedUsage != 110 || snapshot.ResearchReserved != 110 {
		t.Fatalf("snapshot = %#v, want active reservation", snapshot)
	}
}

type fakeUsageRefresher struct {
	pool  *pool.Pool
	used  int64
	err   error
	calls []string
}

func (f *fakeUsageRefresher) RefreshUsage(_ context.Context, name string) error {
	f.calls = append(f.calls, name)
	if f.err != nil {
		return f.err
	}
	f.pool.UpdateUsage(name, pool.Usage{Limit: 1000, Used: f.used}, time.Now())
	return nil
}

func newResearchTestHandler(upstreamURL string, client *http.Client) *Handler {
	keyPool := pool.New([]pool.Key{{Name: "one", APIKey: "tvly-one"}}, 1)
	keyPool.UpdateUsage("one", pool.Usage{Limit: 1000}, time.Now())
	return New("tlk-client", upstreamURL, client, keyPool, 1024, time.Hour)
}
