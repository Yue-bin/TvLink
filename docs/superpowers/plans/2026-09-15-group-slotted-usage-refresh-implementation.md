# 按组错峰的用量刷新实施计划

> For agentic workers: 按任务逐项执行，每步先写失败测试再实现；每任务结束跑 `go test ./...`。

### Task 1: 池内刷新调度核心

**Files:**
- Modify: `internal/pool/pool.go`
- Modify: `internal/pool/pool_test.go`

- [x] 失败测试：`ConfigureRefresh` + `RebuildGroups` 后各组 Key 的到期时刻按槽位错开（组 0 ≈ now，组 1 ≈ now + 周期/组数，含 ±10% 与 0–10s 抖动界）；未分组时按配置顺序铺开；`DueKeys` 只返回到期 Key 并给出下一个到期前的等待时长。
- [x] 失败测试：`RecordRefreshAttempt` 三种退避（成功=周期+抖动、失败=2 分钟、429=max(Retry-After,300s)）；重建重排不越过节流下限；`Refreshable` 判定。
- [x] 失败测试：指标窗口（近周期请求数、60 秒最大连发、近 10 分钟 429 计数）与批次状态（`RecordRefreshBatch` 的 size/failures/error）。
- [x] 实现：`keyState` 增加下次可刷新时刻与节流下限；`RebuildGroups` 重排槽位；请求/429 环形缓冲；`RefreshStatus` 扩展字段。

### Task 2: 刷新客户端记录每次尝试

**Files:**
- Modify: `internal/tavily/client.go`
- Modify: `internal/tavily/client_test.go`

- [x] 失败测试：`RefreshUsage` 在成功、失败、429 三种结果下都向池报告尝试（含 Retry-After 时长）。
- [x] 失败测试：新增 `Refresh(ctx, keys)` 批量入口，逐 Key 执行并合并错误（替代 `RefreshAll`）。

### Task 3: 三个入口改为到期驱动

**Files:**
- Modify: `internal/pool/coordinator.go`
- Modify: `cmd/tvlink/main.go`
- Modify: `internal/pool/coordinator_test.go`

- [x] 失败测试：重建后的后台补刷只刷到期 Key（无到期则不发请求）。
- [x] 实现：`UsageRefresher` 签名改为接收到期 Key 列表；`refreshLoop` 重写为「取到期集合 → 刷新 → 睡到下一个到期」；启动全量刷新保留并随后重排槽位。

### Task 4: Research 结算遵守节流

**Files:**
- Modify: `internal/proxy/research.go`
- Modify: `internal/proxy/research_test.go`

- [x] 失败测试：Key 处于节流期内时结算跳过 `/usage` 并记录日志。
- [x] 实现：`settleResearch` 用 `Refreshable` 守卫。

### Task 5: 监控指标

**Files:**
- Modify: `internal/monitor/view.go`
- Modify: `internal/monitor/template.go`
- Modify: `internal/monitor/view_test.go`
- Modify: `internal/monitor/handler_test.go`

- [x] 失败测试：状态条显示批结果（`N 个 Key` / `F/N 个 Key 失败`），指标行显示三条数字，429 计数 > 0 时告警态。
- [x] 实现：`statusView` 增加指标段；模板渲染。

### Task 6: 验证与提交

- [x] `gofmt` / `go vet` / `go test ./...` 全绿。
- [x] 按原子化中文约定式提交（文档与代码分开，每个提交可独立编译）。
