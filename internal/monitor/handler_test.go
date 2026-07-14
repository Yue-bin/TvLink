package monitor

import (
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tvlink/internal/pool"
)

func TestHandlerRendersUsageDashboard(t *testing.T) {
	now := time.Now()
	p := pool.New([]pool.Key{
		{Name: "primary-01", APIKey: "tvly-secret-one"},
		{Name: "primary-02", APIKey: "tvly-secret-two"},
	}, 1)
	p.UpdateUsage("primary-01", pool.Usage{Limit: 100, Used: 20}, now)
	p.UpdateUsage("primary-02", pool.Usage{Limit: 200, Used: 40}, now)
	if err := p.ConfigureGroups(pool.GroupConfig{Size: 1, UsageLimit: 10, Location: time.UTC}); err != nil {
		t.Fatalf("ConfigureGroups() error = %v", err)
	}
	if err := p.RebuildGroups(now); err != nil {
		t.Fatalf("RebuildGroups() error = %v", err)
	}
	if _, err := p.Select(now, 3); err != nil {
		t.Fatalf("Select() error = %v", err)
	}

	response := httptest.NewRecorder()
	New(p).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	body := html.UnescapeString(response.Body.String())
	for _, text := range []string{
		"TvLink 用量监控", "总用量", "60 (+3) / 300", "primary-01", "primary-02",
		"usage-progress", "progress-actual", "progress-projected", "预计剩余", "Group 1",
		"group-filter", "group-select", "function setFilter", "data-group=\"group-",
	} {
		if !strings.Contains(body, text) {
			t.Errorf("page does not contain %q", text)
		}
	}
	for _, removed := range []string{"http-equiv=\"refresh\"", "自动刷新"} {
		if strings.Contains(body, removed) {
			t.Errorf("page unexpectedly contains %q", removed)
		}
	}
	for _, secret := range []string{"tvly-secret-one", "tvly-secret-two"} {
		if strings.Contains(body, secret) {
			t.Errorf("page exposes Tavily secret %q", secret)
		}
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
	if response.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", response.Header().Get("Content-Type"))
	}
}

func TestHandlerRejectsNonGET(t *testing.T) {
	p := pool.New(nil, 1)
	response := httptest.NewRecorder()
	New(p).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandlerRendersEmptyState(t *testing.T) {
	p := pool.New(nil, 1)
	response := httptest.NewRecorder()
	New(p).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(response.Body.String(), "尚未配置可监控的 Key") {
		t.Fatal("page does not contain empty state")
	}
}
