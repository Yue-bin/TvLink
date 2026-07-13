package monitor

import "html/template"

var pageTemplate = template.Must(template.New("monitor").Parse(pageHTML))

const pageHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta http-equiv="refresh" content="{{.RefreshSeconds}}">
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
    .shell {
      width: min(980px, calc(100% - 36px));
      margin: 0 auto;
      padding: 32px 0 48px;
    }
    .topbar {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 16px;
      margin-bottom: 18px;
    }
    .identity h1 {
      margin: 0;
      font-size: 21px;
      font-weight: 740;
    }
    .live {
      display: flex;
      align-items: center;
      gap: 7px;
      color: var(--muted);
      font-size: 10px;
      text-transform: uppercase;
    }
    .live-dot {
      width: 7px;
      height: 7px;
      border-radius: 50%;
      background: var(--ready);
      box-shadow: 0 0 0 3px rgba(119, 210, 173, .09);
    }
    .summary {
      padding: 18px;
      background: var(--panel);
      border: 1px solid var(--edge);
      border-radius: 7px;
    }
    .summary-top,
    .row-top {
      display: flex;
      align-items: end;
      justify-content: space-between;
      gap: 14px;
    }
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
    .progress-projected {
      position: absolute;
      z-index: 1;
      inset: -1px auto -1px -1px;
      border: 1px dashed var(--estimate);
      border-radius: 3px;
      background: rgba(182, 217, 233, .035);
    }
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
    .usage-progress.unavailable { opacity: .45; }
    .summary-meta,
    .row-meta {
      display: flex;
      flex-wrap: wrap;
      gap: 7px 16px;
      margin-top: 12px;
      color: var(--muted);
      font-size: 10px;
    }
    .summary-meta strong,
    .row-meta strong {
      color: var(--text);
      font-weight: 650;
    }
    .legend-mark {
      display: inline-block;
      width: 13px;
      height: 7px;
      margin-right: 5px;
      vertical-align: -1px;
      border-radius: 1px;
    }
    .legend-actual {
      background: var(--actual);
      border: 1px solid var(--actual-edge);
    }
    .legend-projected { border: 1px dashed var(--estimate); }
    .key-list {
      margin-top: 17px;
      border-top: 1px solid var(--edge);
    }
    .key-row {
      padding: 16px 2px 15px;
      border-bottom: 1px solid var(--edge);
    }
    .key-line {
      display: flex;
      align-items: center;
      gap: 9px;
      min-width: 0;
    }
    .key-name {
      overflow: hidden;
      font-size: 13px;
      font-weight: 700;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
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
    .state-pending,
    .state-probing {
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
    .empty {
      padding: 32px 0;
      color: var(--muted);
      font-size: 13px;
      text-align: center;
    }
    @media (max-width: 620px) {
      .shell {
        width: min(calc(100% - 20px), 980px);
        padding-top: 22px;
      }
      .topbar,
      .summary-top,
      .row-top {
        align-items: flex-start;
        flex-direction: column;
      }
      .usage-number { font-size: 23px; }
      .row-usage { white-space: normal; }
      .summary-meta,
      .row-meta { column-gap: 12px; }
    }
  </style>
</head>
<body>
  <main class="shell">
    <header class="topbar">
      <div class="identity"><h1>TvLink 用量监控</h1></div>
      <div class="live"><i class="live-dot"></i>自动刷新 {{.RefreshSeconds}} 秒</div>
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
      </div>
    </section>
    {{if .Empty}}
    <div class="empty">尚未配置可监控的 Key</div>
    {{else}}
    <section class="key-list">
      {{range .Rows}}
      <article class="key-row">
        <div class="row-top">
          <div class="key-line"><span class="key-name">{{.Name}}</span><span class="status {{.StateClass}}">{{.State}}</span></div>
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
    </section>
    {{end}}
  </main>
</body>
</html>`
