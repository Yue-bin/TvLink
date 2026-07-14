// Package monitor renders TvLink's public operational status page.
package monitor

import (
	"net/http"
	"time"

	"tvlink/internal/pool"
)

// Handler renders public pool snapshots.
type Handler struct {
	pool *pool.Pool
}

// New creates a public monitor handler.
func New(keyPool *pool.Pool) *Handler {
	return &Handler{pool: keyPool}
}

// ServeHTTP renders a non-cacheable status page.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	now := time.Now()
	view := newPageView(h.pool.MonitorSnapshot(now), now)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pageTemplate.Execute(w, view); err != nil {
		http.Error(w, "render monitor", http.StatusInternalServerError)
	}
}
