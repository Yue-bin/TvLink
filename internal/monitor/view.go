package monitor

import (
	"fmt"
	"html/template"
	"math"
	"strconv"
	"strings"
	"time"

	"tvlink/internal/pool"
)

type progressView struct {
	UsageText            string
	ActualWidth          template.CSS
	ProjectedWidth       template.CSS
	ActualPercentText    string
	ProjectedPercentText string
	AriaLabel            string
	Unavailable          bool
}

type keyView struct {
	Name       string
	State      string
	StateClass string
	Metrics    progressView
	UpdatedAt  string
	Remaining  string
	Weight     string
	RetryAt    string
	ShowRetry  bool
}

type pageView struct {
	RefreshSeconds     int64
	Total              progressView
	ProjectedRemaining string
	AvailableKeys      int
	TotalKeys          int
	Rows               []keyView
	Empty              bool
}

func newPageView(snapshots []pool.Snapshot, refreshInterval time.Duration, _ time.Time) pageView {
	view := pageView{
		RefreshSeconds: int64(refreshInterval.Seconds()),
		TotalKeys:      len(snapshots),
		Rows:           make([]keyView, 0, len(snapshots)),
		Empty:          len(snapshots) == 0,
	}
	var totalLimit, totalActual int64
	var totalEstimated, totalRemaining float64
	for _, snapshot := range snapshots {
		totalLimit += snapshot.Limit
		totalActual += snapshot.RealUsage
		totalEstimated += snapshot.EstimatedUsage
		totalRemaining += snapshot.Remaining
		if snapshot.Weight > 0 {
			view.AvailableKeys++
		}
		view.Rows = append(view.Rows, keyView{
			Name:       snapshot.Name,
			State:      string(snapshot.State),
			StateClass: "state-" + string(snapshot.State),
			Metrics:    newProgressView(snapshot.RealUsage, snapshot.EstimatedUsage, snapshot.Limit),
			UpdatedAt:  formatTimestamp(snapshot.RealUsageAt),
			Remaining:  formatFloat(snapshot.Remaining),
			Weight:     formatFloat(snapshot.Weight),
			RetryAt:    formatTimestamp(snapshot.RetryAt),
			ShowRetry:  snapshot.State == pool.StateCooling,
		})
	}
	view.Total = newProgressView(totalActual, totalEstimated, totalLimit)
	view.ProjectedRemaining = formatFloat(totalRemaining)
	return view
}

func newProgressView(actual int64, estimated float64, limit int64) progressView {
	actualPercent := percentage(float64(actual), limit)
	projectedPercent := percentage(float64(actual)+estimated, limit)
	return progressView{
		UsageText:            fmt.Sprintf("%s (+%s) / %s", formatInt(actual), formatFloat(estimated), formatInt(limit)),
		ActualWidth:          template.CSS(fmt.Sprintf("width:%.2f%%", actualPercent)),
		ProjectedWidth:       template.CSS(fmt.Sprintf("width:%.2f%%", projectedPercent)),
		ActualPercentText:    formatPercent(actualPercent),
		ProjectedPercentText: formatPercent(projectedPercent),
		AriaLabel:            fmt.Sprintf("实际用量 %s，预计总用量 %s，额度 %s", formatInt(actual), formatFloat(float64(actual)+estimated), formatInt(limit)),
		Unavailable:          limit <= 0,
	}
}

func percentage(value float64, limit int64) float64 {
	if limit <= 0 {
		return 0
	}
	return min(100, max(0, value/float64(limit)*100))
}

func formatPercent(value float64) string {
	return formatFloat(value) + "%"
}

func formatTimestamp(value time.Time) string {
	if value.IsZero() {
		return "--"
	}
	return value.Local().Format("01-02 15:04:05")
}

func formatInt(value int64) string {
	return groupInteger(strconv.FormatInt(value, 10))
}

func formatFloat(value float64) string {
	rounded := math.Round(value*100) / 100
	raw := strconv.FormatFloat(rounded, 'f', 2, 64)
	raw = strings.TrimRight(strings.TrimRight(raw, "0"), ".")
	parts := strings.SplitN(raw, ".", 2)
	parts[0] = groupInteger(parts[0])
	return strings.Join(parts, ".")
}

func groupInteger(value string) string {
	sign := ""
	if strings.HasPrefix(value, "-") {
		sign, value = "-", strings.TrimPrefix(value, "-")
	}
	for index := len(value) - 3; index > 0; index -= 3 {
		value = value[:index] + "," + value[index:]
	}
	return sign + value
}
