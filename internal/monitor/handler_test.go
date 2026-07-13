package monitor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tvlink/internal/pool"
)

func TestHandlerRendersActualAndEstimatedUsageSeparately(t *testing.T) {
	p := pool.New([]pool.Key{{Name: "primary-01", APIKey: "tvly-secret"}}, 1)
	p.UpdateUsage("primary-01", pool.Usage{Limit: 100, Used: 20}, time.Now())
	if _, err := p.Select(time.Now(), 3); err != nil {
		t.Fatalf("Select() error = %v", err)
	}

	response := httptest.NewRecorder()
	New(p, 5*time.Second).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	for _, text := range []string{"真实用量", "真实用量获取时间", "推测用量", "primary-01"} {
		if !strings.Contains(response.Body.String(), text) {
			t.Errorf("page does not contain %q", text)
		}
	}
	if strings.Contains(response.Body.String(), "tvly-secret") {
		t.Error("page exposes Tavily secret")
	}
}
