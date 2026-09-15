package tavily

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tvlink/internal/pool"
)

func TestRefreshUsageUpdatesPool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/usage" {
			t.Errorf("path = %q, want /usage", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tvly-one" {
			t.Errorf("Authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"key":{"usage":12,"limit":null},"account":{"plan_usage":12,"plan_limit":100,"paygo_usage":0,"paygo_limit":null}}`))
	}))
	defer server.Close()

	p := pool.New([]pool.Key{{Name: "one", APIKey: "tvly-one"}}, 1)
	client := NewClient(server.URL, server.Client(), p, []pool.Key{{Name: "one", APIKey: "tvly-one"}})
	if err := client.RefreshUsage(context.Background(), "one"); err != nil {
		t.Fatalf("RefreshUsage() error = %v", err)
	}

	snapshot := p.Snapshots(time.Now())[0]
	if snapshot.RealUsage != 12 || snapshot.Limit != 100 {
		t.Errorf("snapshot = %+v, want usage 12 and limit 100", snapshot)
	}
	if snapshot.RealUsageAt.IsZero() {
		t.Error("RealUsageAt is zero")
	}
}

func TestRefreshUsageReturnsRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	p := pool.New([]pool.Key{{Name: "one", APIKey: "tvly-one"}}, 1)
	client := NewClient(server.URL, server.Client(), p, []pool.Key{{Name: "one", APIKey: "tvly-one"}})
	err := client.RefreshUsage(context.Background(), "one")
	if retryAfter, ok := RetryAfter(err); !ok || retryAfter != time.Minute {
		t.Fatalf("RetryAfter() = (%s, %t), want (1m0s, true)", retryAfter, ok)
	}
}

func TestRefreshUsageRecordsAttemptsInPool(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"key":{"usage":12,"limit":null},"account":{"plan_usage":12,"plan_limit":100,"paygo_usage":0,"paygo_limit":null}}`))
	}))
	defer healthy.Close()
	limited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer limited.Close()

	keys := []pool.Key{{Name: "one", APIKey: "tvly-one"}}
	p := pool.New(keys, 1)
	if err := p.ConfigureRefresh(time.Hour); err != nil {
		t.Fatal(err)
	}

	client := NewClient(healthy.URL, healthy.Client(), p, keys)
	if err := client.RefreshUsage(context.Background(), "one"); err != nil {
		t.Fatalf("RefreshUsage() error = %v", err)
	}
	if status := p.MonitorSnapshot(time.Now()).Refresh; status.Requests != 1 || status.RateLimited != 0 {
		t.Fatalf("metrics after a success = %+v, want 1 request and no rate limit", status)
	}
	if p.Refreshable("one", time.Now()) {
		t.Error("Key is refreshable right after a success, want it throttled")
	}

	throttled := NewClient(limited.URL, limited.Client(), p, keys)
	if _, ok := RetryAfter(throttled.RefreshUsage(context.Background(), "one")); !ok {
		t.Fatal("RefreshUsage() did not report throttling")
	}
	if status := p.MonitorSnapshot(time.Now()).Refresh; status.Requests != 2 || status.RateLimited != 1 {
		t.Fatalf("metrics after 429 = %+v, want 2 requests and 1 rate limit", status)
	}
	if p.Refreshable("one", time.Now().Add(time.Minute)) {
		t.Error("Key is refreshable inside the 429 backoff, want it throttled")
	}
	if !p.Refreshable("one", time.Now().Add(5*time.Minute+time.Second)) {
		t.Error("Key is not refreshable after the 429 backoff")
	}
}

func TestRefreshReportsEachKeyFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer tvly-two" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"key":{"usage":1,"limit":null},"account":{"plan_usage":1,"plan_limit":100}}`))
	}))
	defer server.Close()

	keys := []pool.Key{{Name: "one", APIKey: "tvly-one"}, {Name: "two", APIKey: "tvly-two"}}
	p := pool.New(keys, 1)
	client := NewClient(server.URL, server.Client(), p, keys)
	err := client.Refresh(context.Background(), keys)
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("Refresh() error = %v, want the failing Key reported", err)
	}
	snapshots := p.Snapshots(time.Now())
	if snapshots[0].RealUsage != 1 {
		t.Errorf("healthy Key snapshot = %+v, want the successful refresh applied", snapshots[0])
	}
	if snapshots[1].RealUsage != 0 {
		t.Errorf("failed Key snapshot = %+v, want no usage recorded", snapshots[1])
	}
}

func TestEffectiveUsageUsesTighterKeyLimit(t *testing.T) {
	keyLimit := int64(100)
	planLimit := int64(1_000)
	limit, used, err := effectiveUsage(usageResponse{
		Key: struct {
			Usage int64  "json:\"usage\""
			Limit *int64 "json:\"limit\""
		}{Usage: 90, Limit: &keyLimit},
		Account: struct {
			PlanUsage  int64  "json:\"plan_usage\""
			PlanLimit  *int64 "json:\"plan_limit\""
			PaygoUsage int64  "json:\"paygo_usage\""
			PaygoLimit *int64 "json:\"paygo_limit\""
		}{PlanUsage: 100, PlanLimit: &planLimit},
	})
	if err != nil {
		t.Fatalf("effectiveUsage() error = %v", err)
	}
	if limit != 100 || used != 90 {
		t.Errorf("effectiveUsage() = (%d, %d), want (100, 90)", limit, used)
	}
}
