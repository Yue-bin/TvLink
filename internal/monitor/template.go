package monitor

import "html/template"

var pageTemplate = template.Must(template.New("monitor").Parse(pageHTML))

const pageHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="color-scheme" content="dark">
  <title>TvLink 监控</title>
  <style>
    :root {
      color-scheme: dark;
      --page: #090e12;
      --panel: #111920;
      --panel-2: #0d1318;
      --edge: #283946;
      --edge-strong: #385062;
      --text: #eef5f8;
      --muted: #8598a4;
      --accent: #70c9f3;
      --actual: #203b4b;
      --actual-edge: #4e7890;
      --estimate: #b6d9e9;
      --track: #060a0d;
      --ready: #77d2ad;
      --warning: #ffb55f;
      --danger: #ff9080;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      background: var(--page);
      color: var(--text);
      font-family: Inter, ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif;
      letter-spacing: 0;
    }
    button, select { font: inherit; }
    .shell {
      width: min(1120px, calc(100% - 36px));
      margin: 0 auto;
      padding: 32px 0 48px;
    }
    .topbar, .summary-top, .row-top, .key-heading, .group-top {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 14px;
    }
    .topbar { margin-bottom: 18px; }
    .identity h1 { margin: 0; font-size: 21px; font-weight: 740; }
    .snapshot { color: var(--muted); font-size: 10px; }
    .summary {
      padding: 18px;
      background: var(--panel);
      border: 1px solid var(--edge);
      border-radius: 7px;
    }
    .summary-top, .row-top, .key-heading { align-items: end; }
    .eyebrow {
      color: var(--muted);
      font-size: 10px;
      font-weight: 700;
      text-transform: uppercase;
    }
    .usage-number {
      margin-top: 6px;
      font-size: 28px;
      line-height: 1;
      font-weight: 760;
      font-variant-numeric: tabular-nums;
    }
    .projected-percent {
      color: var(--accent);
      font-size: 14px;
      font-weight: 700;
      white-space: nowrap;
    }
    .usage-progress {
      position: relative;
      height: 14px;
      margin-top: 16px;
      overflow: hidden;
      background: var(--track);
      border: 1px solid #17232b;
      border-radius: 3px;
    }
    .usage-progress.compact { height: 9px; margin-top: 8px; }
    .progress-projected {
      position: absolute;
      z-index: 1;
      top: 3px;
      bottom: 3px;
      left: 0;
      border-right: 1.5px solid var(--estimate);
      background: rgba(182, 217, 233, .06);
    }
    .compact .progress-projected { top: 2px; bottom: 2px; }
    .progress-actual {
      position: absolute;
      z-index: 2;
      top: 3px;
      bottom: 3px;
      left: 0;
      background: var(--actual);
      border: 1px solid var(--actual-edge);
      border-radius: 2px;
    }
    .compact .progress-actual { top: 2px; bottom: 2px; }
    .usage-progress.unavailable { opacity: .45; }
    .summary-meta, .row-meta, .group-meta {
      display: flex;
      flex-wrap: wrap;
      gap: 7px 16px;
      margin-top: 12px;
      color: var(--muted);
      font-size: 10px;
    }
    .summary-meta strong, .row-meta strong { color: var(--text); font-weight: 650; }
    .legend-mark {
      display: inline-block;
      width: 13px;
      height: 7px;
      margin-right: 5px;
      vertical-align: -1px;
      border-radius: 1px;
    }
    .legend-actual { background: var(--actual); border: 1px solid var(--actual-edge); }
    .legend-projected { background: rgba(182, 217, 233, .06); border-right: 1.5px solid var(--estimate); }
    .workspace { margin-top: 18px; }
    .workspace.grouped {
      display: grid;
      grid-template-columns: 270px minmax(0, 1fr);
      gap: 22px;
    }
    .group-panel, .key-panel { min-width: 0; }
    .panel-title {
      display: flex;
      align-items: center;
      justify-content: space-between;
      padding: 0 2px 9px;
      color: var(--muted);
      font-size: 10px;
      font-weight: 700;
      text-transform: uppercase;
    }
    .group-filter {
      display: flex;
      flex-direction: column;
      max-height: 730px;
      overflow: auto;
      border-top: 1px solid var(--edge);
      border-bottom: 1px solid var(--edge);
      scrollbar-color: var(--edge-strong) transparent;
    }
    .group-option {
      width: 100%;
      padding: 12px 10px;
      border: 0;
      border-bottom: 1px solid var(--edge);
      background: transparent;
      color: inherit;
      text-align: left;
      cursor: pointer;
    }
    .group-option:last-child { border-bottom: 0; }
    .group-option:hover { background: rgba(112, 201, 243, .03); }
    .group-option.active {
      background: rgba(112, 201, 243, .055);
      box-shadow: inset 2px 0 var(--accent);
    }
    .group-option:focus-visible { outline: 1px solid var(--accent); outline-offset: -1px; }
    .group-name { font-size: 12px; font-weight: 760; }
    .group-state { color: var(--muted); font-size: 9px; }
    .group-active .group-state { color: var(--accent); }
    .group-spent .group-state { color: var(--ready); }
    .group-usage {
      margin-top: 7px;
      color: var(--muted);
      font-size: 9px;
      font-variant-numeric: tabular-nums;
    }
    .group-option.active .group-usage { color: var(--text); }
    .group-meta {
      justify-content: space-between;
      gap: 8px;
      margin-top: 7px;
      font-size: 9px;
    }
    .all-option { padding: 13px 10px; }
    .all-option .group-name { font-size: 13px; }
    .all-option .group-meta { justify-content: flex-start; gap: 14px; }
    .mini-axis-card {
      padding: 13px 14px 12px;
      margin-bottom: 14px;
      background: var(--panel);
      border: 1px solid var(--edge);
      border-radius: 7px;
    }
    .ma-head { display: flex; justify-content: space-between; align-items: baseline; }
    .ma-head .ma-t { color: var(--muted); font-size: 9px; font-weight: 700; text-transform: uppercase; letter-spacing: .08em; }
    .ma-head .ma-v { color: var(--accent); font-size: 10px; font-weight: 700; }
    .ma-track { position: relative; display: grid; grid-template-columns: repeat(auto-fit, minmax(0, 1fr)); grid-auto-flow: column; gap: 3px; margin-top: 11px; }
    .ma-cell {
      position: relative;
      display: grid;
      place-items: center;
      height: 18px;
      overflow: hidden;
      background: var(--track);
      border: 1px solid var(--edge);
      border-radius: 3px;
    }
    .ma-cell span { position: relative; z-index: 1; color: var(--muted); font-size: 8px; font-weight: 700; }
    .ma-cell.done { background: rgba(119, 210, 173, .1); border-color: rgba(119, 210, 173, .3); }
    .ma-cell.done span { color: var(--ready); }
    .ma-cell.now { border-color: var(--accent); }
    .ma-cell.now .ma-fill { position: absolute; top: 0; bottom: 0; left: 0; background: rgba(112, 201, 243, .18); }
    .ma-cell.now span { color: var(--accent); }
    .ma-cursor { position: absolute; z-index: 2; top: -3px; bottom: -3px; width: 2px; background: var(--accent); }
    .ma-cursor::before {
      content: "";
      position: absolute;
      top: -3.5px;
      left: 50%;
      transform: translateX(-50%);
      border: 3px solid transparent;
      border-top-color: var(--accent);
    }
    .ma-detail {
      display: grid;
      grid-template-columns: 1fr 1fr;
      gap: 5px 12px;
      margin-top: 11px;
      padding-top: 10px;
      border-top: 1px solid var(--edge);
    }
    .ma-detail .ma-r { display: flex; justify-content: space-between; color: var(--muted); font-size: 9px; }
    .ma-detail .ma-r b { color: var(--text); font-weight: 600; font-variant-numeric: tabular-nums; }
    .mobile-filter { display: none; margin-top: 18px; }
    .mobile-filter label {
      display: block;
      margin-bottom: 7px;
      color: var(--muted);
      font-size: 10px;
      font-weight: 700;
      text-transform: uppercase;
    }
    .mobile-filter select {
      width: 100%;
      height: 40px;
      padding: 0 10px;
      border: 1px solid var(--edge-strong);
      border-radius: 4px;
      background: var(--panel);
      color: var(--text);
      font-size: 12px;
    }
    .key-heading {
      padding: 0 2px 9px;
      border-bottom: 1px solid var(--edge);
    }
    .key-heading h2 { margin: 0; font-size: 14px; }
    .key-heading p { margin: 4px 0 0; color: var(--muted); font-size: 10px; }
    .filter-summary { color: var(--muted); font-size: 10px; text-align: right; }
    .filter-summary strong { color: var(--text); }
    .key-row { padding: 16px 2px 15px; border-bottom: 1px solid var(--edge); }
    .key-line { display: flex; align-items: center; gap: 9px; min-width: 0; }
    .key-name {
      overflow: hidden;
      font-size: 13px;
      font-weight: 700;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    .group-badge { color: var(--muted); font-size: 9px; }
    .status {
      padding: 3px 6px;
      border-radius: 4px;
      font-size: 9px;
      font-weight: 760;
      text-transform: uppercase;
      white-space: nowrap;
    }
    .state-ready {
      color: var(--ready);
      border: 1px solid rgba(119, 210, 173, .24);
      background: rgba(119, 210, 173, .09);
    }
    .state-cooling {
      color: var(--warning);
      border: 1px solid rgba(255, 181, 95, .24);
      background: rgba(255, 181, 95, .08);
    }
    .state-exhausted {
      color: var(--danger);
      border: 1px solid rgba(255, 144, 128, .24);
      background: rgba(255, 144, 128, .08);
    }
    .state-pending, .state-probing {
      color: var(--muted);
      border: 1px solid var(--edge-strong);
      background: var(--panel-2);
    }
    .row-usage {
      font-size: 12px;
      font-weight: 700;
      font-variant-numeric: tabular-nums;
      white-space: nowrap;
    }
    .empty { padding: 42px 0; color: var(--muted); font-size: 12px; text-align: center; }
    [hidden] { display: none !important; }
    @media (max-width: 760px) {
      .shell { width: min(calc(100% - 20px), 1120px); padding-top: 22px; }
      .topbar, .summary-top, .row-top, .key-heading {
        align-items: flex-start;
        flex-direction: column;
      }
      .usage-number { font-size: 23px; }
      .row-usage { white-space: normal; }
      .summary-meta, .row-meta { column-gap: 12px; }
      .workspace.grouped { display: block; margin-top: 14px; }
      .group-panel { display: none; }
      .mobile-filter { display: block; }
      .filter-summary { text-align: left; }
      .key-panel { margin-top: 16px; }
      .group-badge { display: none; }
    }
  </style>
</head>
<body>
  <main class="shell">
    <header class="topbar">
      <div class="identity"><h1>TvLink 用量监控</h1></div>
      <div class="snapshot">数据生成于 {{.GeneratedAt}}</div>
    </header>
    <section class="summary">
      <div class="summary-top">
        <div><div class="eyebrow">总用量</div><div class="usage-number">{{.Total.UsageText}}</div></div>
        <div class="projected-percent">预计 {{.Total.ProjectedPercentText}}</div>
      </div>
      <div class="usage-progress{{if .Total.Unavailable}} unavailable{{end}}" role="img" aria-label="{{.Total.AriaLabel}}">
        <div class="progress-projected" style="{{.Total.ProjectedWidth}}"></div>
        <div class="progress-actual" style="{{.Total.ActualWidth}}"></div>
      </div>
      <div class="summary-meta">
        <span><i class="legend-mark legend-actual"></i>实际 <strong>{{.Total.ActualPercentText}}</strong></span>
        <span><i class="legend-mark legend-projected"></i>预计 <strong>{{.Total.ProjectedPercentText}}</strong></span>
        <span>预计剩余 <strong>{{.ProjectedRemaining}}</strong></span>
        <span>可用 Key <strong>{{.AvailableKeys}} / {{.TotalKeys}}</strong></span>
        {{if .GroupingEnabled}}{{range .Groups}}{{if .Active}}
        <span>当前组 <strong>{{.Name}}</strong></span>
        <span>本轮组用量 <strong>{{.RoundMetrics.UsageText}}</strong></span>
        {{end}}{{end}}{{end}}
      </div>
    </section>

    {{if .GroupingEnabled}}
    <div class="mobile-filter">
      <label for="group-select">显示范围</label>
      <select id="group-select" onchange="setFilter(this.value)">
        <option value="all">所有 Key</option>
        {{range .Groups}}<option value="{{.ID}}">{{.Name}}{{if .Active}} · 当前活动{{end}}</option>{{end}}
      </select>
    </div>
    {{end}}

    <div class="workspace{{if .GroupingEnabled}} grouped{{end}}">
      {{if .GroupingEnabled}}
      <aside class="group-panel">
        <div class="panel-title"><span>显示范围</span><span>{{len .Groups}} 组</span></div>
        {{if .HasActiveGroup}}
        <div class="mini-axis-card">
          <div class="ma-head"><span class="ma-t">轮换进度</span><span class="ma-v">{{.Rotation.ActiveName}} · {{.Rotation.ActivePercent}}</span></div>
          <div class="ma-track">
            {{range .Groups}}
            <div class="ma-cell{{if .Spent}} done{{else if .Active}} now{{end}}">
              {{if .Active}}<div class="ma-fill" style="{{.RoundMetrics.ActualWidth}}"></div>{{end}}
              <span>{{if .Spent}}✓{{else}}{{.ShortName}}{{end}}</span>
            </div>
            {{end}}
            <div class="ma-cursor" style="{{.Rotation.CursorLeft}}"></div>
          </div>
          <div class="ma-detail">
            <div class="ma-r"><span>本轮</span><b>{{.Rotation.RoundUsage}}</b></div>
            <div class="ma-r"><span>剩余</span><b>{{.Rotation.RoundLeft}} 次</b></div>
            <div class="ma-r"><span>组 Key 用量</span><b>{{.Rotation.GroupUsage}}</b></div>
            <div class="ma-r"><span>Ready</span><b>{{.Rotation.ReadyText}}</b></div>
          </div>
        </div>
        {{end}}
        <nav class="group-filter" aria-label="Key 分组筛选">
          <button class="group-option all-option active" type="button" data-filter="all" data-title="所有 Key" data-description="按名称展示全部脱敏 Key" onclick="setFilter('all')">
            <div class="group-top"><span class="group-name">所有 Key</span><span class="group-state">{{.TotalKeys}}</span></div>
            <div class="group-meta"><span>预计剩余 {{.ProjectedRemaining}}</span><span>可用 {{.AvailableKeys}}</span></div>
          </button>
          {{range .Groups}}
          <button class="group-option {{.StateClass}}" type="button" data-filter="{{.ID}}" data-title="{{.Name}}" data-description="{{.State}}，包含 {{.KeyCount}} 个 Key" onclick="setFilter('{{.ID}}')">
            <div class="group-top"><span class="group-name">{{.Name}}</span><span class="group-state">{{.State}}</span></div>
            <div class="group-usage">本轮次 {{.RoundMetrics.UsageText}}</div>
            <div class="usage-progress compact{{if .RoundMetrics.Unavailable}} unavailable{{end}}" role="img" aria-label="{{.RoundMetrics.AriaLabel}}">
              <div class="progress-actual" style="{{.RoundMetrics.ActualWidth}}"></div>
            </div>
            <div class="group-meta"><span>Ready {{.ReadyKeys}} / {{.KeyCount}}</span><span>Key 用量 {{.QuotaUsage}}</span><span>预计剩余 {{.Remaining}}</span></div>
          </button>
          {{end}}
        </nav>
      </aside>
      {{end}}

      <section class="key-panel">
        {{if .Empty}}
        <div class="empty">尚未配置可监控的 Key</div>
        {{else}}
        <header class="key-heading">
          <div><h2 id="view-title">所有 Key</h2><p id="view-description">按名称展示全部脱敏 Key</p></div>
          <div class="filter-summary"><strong id="visible-count">{{.TotalKeys}}</strong> 个 Key</div>
        </header>
        {{range .Rows}}
        <article class="key-row"{{if .GroupID}} data-group="{{.GroupID}}"{{end}}>
          <div class="row-top">
            <div class="key-line"><span class="key-name">{{.Name}}</span><span class="status {{.StateClass}}">{{.State}}</span>{{if $.GroupingEnabled}}<span class="group-badge">{{.GroupName}}</span>{{end}}</div>
            <span class="row-usage">{{.Metrics.UsageText}}</span>
          </div>
          <div class="usage-progress{{if .Metrics.Unavailable}} unavailable{{end}}" role="img" aria-label="{{.Metrics.AriaLabel}}">
            <div class="progress-projected" style="{{.Metrics.ProjectedWidth}}"></div>
            <div class="progress-actual" style="{{.Metrics.ActualWidth}}"></div>
          </div>
          <div class="row-meta">
            <span>实际更新 <strong>{{.UpdatedAt}}</strong></span>
            <span>预计剩余 <strong>{{.Remaining}}</strong></span>
            <span>权重 <strong>{{.Weight}}</strong></span>
            {{if .ShowRetry}}<span>重试时间 <strong>{{.RetryAt}}</strong></span>{{end}}
          </div>
        </article>
        {{end}}
        <div class="empty" id="filter-empty" hidden>该组没有可展示的 Key</div>
        {{end}}
      </section>
    </div>
  </main>
  {{if .GroupingEnabled}}
  <script>
    function setFilter(filter) {
      var rows = Array.from(document.querySelectorAll(".key-row"));
      var visible = 0;
      rows.forEach(function (row) {
        var show = filter === "all" || row.dataset.group === filter;
        row.hidden = !show;
        if (show) visible++;
      });
      document.querySelectorAll(".group-option").forEach(function (option) {
        option.classList.toggle("active", option.dataset.filter === filter);
      });
      document.getElementById("group-select").value = filter;
      var selected = document.querySelector('.group-option[data-filter="' + filter + '"]');
      document.getElementById("view-title").textContent = selected.dataset.title;
      document.getElementById("view-description").textContent = selected.dataset.description;
      document.getElementById("visible-count").textContent = visible;
      document.getElementById("filter-empty").hidden = visible !== 0;
    }
  </script>
  {{end}}
</body>
</html>`
