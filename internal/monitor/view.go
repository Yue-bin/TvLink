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
	GroupID    string
	GroupName  string
	State      string
	StateClass string
	Metrics    progressView
	UpdatedAt  string
	Remaining  string
	Weight     string
	RetryAt    string
	ShowRetry  bool
}

type groupView struct {
	ID           string
	Name         string
	ShortName    string
	State        string
	StateClass   string
	Active       bool
	Spent        bool
	RoundMetrics progressView
	QuotaUsage   string
	KeyCount     int
	ReadyKeys    int
	Remaining    string
}

type rotationView struct {
	CursorLeft    template.CSS
	ActiveName    string
	ActivePercent string
	RoundUsage    string
	RoundLeft     string
	GroupUsage    string
	ReadyText     string
}

type pageView struct {
	GeneratedAt        string
	Total              progressView
	ProjectedRemaining string
	AvailableKeys      int
	TotalKeys          int
	Rows               []keyView
	Groups             []groupView
	GroupingEnabled    bool
	ActiveGroupName    string
	Rotation           rotationView
	HasActiveGroup     bool
	Empty              bool
}

func newPageView(snapshot pool.MonitorSnapshot, now time.Time) pageView {
	view := pageView{
		GeneratedAt:     formatTimestamp(now),
		TotalKeys:       len(snapshot.Keys),
		Rows:            make([]keyView, 0, len(snapshot.Keys)),
		Groups:          make([]groupView, 0, len(snapshot.Groups)),
		GroupingEnabled: snapshot.GroupingEnabled && len(snapshot.Groups) > 0,
		ActiveGroupName: "--",
		Empty:           len(snapshot.Keys) == 0,
	}
	var totalLimit, totalActual int64
	var totalEstimated, totalRemaining float64
	for _, key := range snapshot.Keys {
		totalLimit += key.Limit
		totalActual += key.RealUsage
		totalEstimated += key.EstimatedUsage
		totalRemaining += key.Remaining
		if key.State == pool.StateReady {
			view.AvailableKeys++
		}
		metrics := newProgressView(key.RealUsage, key.EstimatedUsage, key.Limit)
		if metrics.Unavailable {
			metrics.UsageText = "尚无用量数据"
			metrics.AriaLabel = "用量数据尚不可用"
		}
		groupID, groupName := "", ""
		if key.Group > 0 {
			groupID = fmt.Sprintf("group-%d", key.Group)
			groupName = fmt.Sprintf("Group %d", key.Group)
		}
		view.Rows = append(view.Rows, keyView{
			Name:       key.Name,
			GroupID:    groupID,
			GroupName:  groupName,
			State:      string(key.State),
			StateClass: "state-" + string(key.State),
			Metrics:    metrics,
			UpdatedAt:  formatTimestamp(key.RealUsageAt),
			Remaining:  formatFloat(key.Remaining),
			Weight:     formatFloat(key.Weight),
			RetryAt:    formatTimestamp(key.RetryAt),
			ShowRetry:  key.State == pool.StateCooling,
		})
	}
	for _, group := range snapshot.Groups {
		state, stateClass := "等待", "group-waiting"
		if group.Spent {
			state, stateClass = "本轮完成", "group-spent"
		}
		if group.Active {
			state, stateClass = "当前活动", "group-active"
		}
		name := fmt.Sprintf("Group %d", group.Index)
		view.Groups = append(view.Groups, groupView{
			ID:           fmt.Sprintf("group-%d", group.Index),
			Name:         name,
			ShortName:    strconv.Itoa(group.Index),
			State:        state,
			StateClass:   stateClass,
			Active:       group.Active,
			Spent:        group.Spent,
			RoundMetrics: newRoundProgressView(group.RoundUsage, group.RoundLimit),
			QuotaUsage:   newProgressView(group.RealUsage, group.EstimatedUsage, group.Limit).UsageText,
			KeyCount:     group.KeyCount,
			ReadyKeys:    group.ReadyKeys,
			Remaining:    formatFloat(group.Remaining),
		})
		if group.Active {
			view.ActiveGroupName = name
			view.HasActiveGroup = true
			roundPercent := percentageOf(group.RoundUsage, group.RoundLimit)
			groupWidth := 100 / float64(len(snapshot.Groups))
			cursor := (float64(group.Index-1) + roundPercent/100) * groupWidth
			view.Rotation = rotationView{
				CursorLeft:    template.CSS(fmt.Sprintf("left:%.2f%%", cursor)),
				ActiveName:    name,
				ActivePercent: formatPercent(roundPercent),
				RoundUsage:    fmt.Sprintf("%s / %s", formatFloat(group.RoundUsage), formatFloat(group.RoundLimit)),
				RoundLeft:     formatFloat(max(0, group.RoundLimit-group.RoundUsage)),
				GroupUsage:    newProgressView(group.RealUsage, group.EstimatedUsage, group.Limit).UsageText,
				ReadyText:     fmt.Sprintf("%d / %d", group.ReadyKeys, group.KeyCount),
			}
		}
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
	return percentageOf(value, float64(limit))
}

func newRoundProgressView(usage, limit float64) progressView {
	percent := percentageOf(usage, limit)
	return progressView{
		UsageText:         fmt.Sprintf("%s / %s", formatFloat(usage), formatFloat(limit)),
		ActualWidth:       template.CSS(fmt.Sprintf("width:%.2f%%", percent)),
		ActualPercentText: formatPercent(percent),
		AriaLabel:         fmt.Sprintf("本轮次已使用 %s，限额 %s", formatFloat(usage), formatFloat(limit)),
		Unavailable:       limit <= 0,
	}
}

func percentageOf(value, limit float64) float64 {
	if limit <= 0 {
		return 0
	}
	return min(100, max(0, value/limit*100))
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
