package tavily

import (
	"context"
	"net/http"
	"net/http/httptest"
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
