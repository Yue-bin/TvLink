// Package monitor renders TvLink's public operational status page.
package monitor

import (
	"html/template"
	"net/http"
	"time"

	"tvlink/internal/pool"
)

var pageTemplate = template.Must(template.New("monitor").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta http-equiv="refresh" content="{{.RefreshSeconds}}"><title>TvLink 监控</title>
<style>body{font-family:system-ui,sans-serif;margin:2rem;color:#18201d}table{border-collapse:collapse;width:100%}th,td{border-bottom:1px solid #d7ddd9;padding:.6rem;text-align:left}th{background:#f1f5f2}</style></head>
<body><h1>TvLink 用量监控</h1><table><thead><tr><th>Key</th><th>状态</th><th>真实用量</th><th>真实用量获取时间</th><th>推测用量</th><th>预计剩余</th><th>权重</th><th>重试时间</th></tr></thead>
<tbody>{{range .Snapshots}}<tr><td>{{.Name}}</td><td>{{.State}}</td><td>{{.RealUsage}} / {{.Limit}}</td><td>{{.RealUsageAt}}</td><td>{{printf "%.2f" .EstimatedUsage}}</td><td>{{printf "%.2f" .Remaining}}</td><td>{{printf "%.2f" .Weight}}</td><td>{{.RetryAt}}</td></tr>{{end}}</tbody></table></body></html>`))

// Handler renders public pool snapshots.
type Handler struct {
	pool            *pool.Pool
	refreshInterval time.Duration
}

// New creates a public monitor handler.
func New(keyPool *pool.Pool, refreshInterval time.Duration) *Handler {
	return &Handler{pool: keyPool, refreshInterval: refreshInterval}
}

// ServeHTTP renders a non-cacheable status page.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pageTemplate.Execute(w, struct {
		RefreshSeconds int64
		Snapshots      []pool.Snapshot
	}{
		RefreshSeconds: int64(h.refreshInterval.Seconds()),
		Snapshots:      h.pool.Snapshots(time.Now()),
	}); err != nil {
		http.Error(w, "render monitor", http.StatusInternalServerError)
	}
}
