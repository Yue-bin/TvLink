# Tavily `/usage` 轮询相关事实核查

调研日期：2026-08（本文件由子代理调研生成）
用途：为 key 管理代理「多久轮询一次 `/usage`」的设计决策提供依据。

---

## (a) 官方 Tavily 文档已确认的事实

### 1. 速率限制

- **按环境（API key 所属环境）划分，而非按 plan 划分**：
  - Development 环境：**100 RPM**
  - Production 环境：**1,000 RPM**
  - Crawl 端点：Development / Production **均为 100 RPM**
  - 来源：https://docs.tavily.com/documentation/rate-limits 、https://help.tavily.com/articles/3240802908-rate-limits
  - 原文：「Tavily offers two types of rate limits, each specific to the environment associated with your API key.」
- **`/usage` 端点有独立的、更严格的限制**：
  - **每 10 分钟 10 次请求**，Development 与 Production key 相同
  - 来源：https://docs.tavily.com/documentation/rate-limits
  - 原文（搜索片段）：「The usage endpoint has a separate rate limit that applies to both development and production keys: Environment / Requests per 10 minutes / Development 10」
  - 注：抓取到的片段只显式给出了 Development = 10 一行；Production 亦为 10 由第三方来源佐证（见 b 节）。
- **月度额度是账号/plan 级**，按月重置：
  - Free (Researcher)：1,000 credits/月；Project 4,000/$30；Bootstrap 15,000/$100；Startup 38,000/$220；Growth 100,000/$500；Pay-as-you-go $0.008/credit
  - 来源：https://docs.tavily.com/documentation/api-credits
- **免费 vs 付费**：官方文档把差异表述为 **开发/生产环境**（RPM）与 **plan 月度 credits**（额度），并未直接写「free plan 的 RPM 更低」。即 RPM 挂在 key 的环境属性上，月度配额挂在账号 plan 上。

### 2. `GET https://api.tavily.com/usage` 是官方端点

- 官方 API Reference 存在该页：https://docs.tavily.com/documentation/api-reference/endpoint/usage
- 官方文档索引 llms.txt 条目：「Usage: Get API key and account usage details.」 https://docs.tavily.com/llms.txt
- 认证：`Authorization: Bearer <tvly-...>`；不支持 query parameter（401）、不支持 POST（405）。
- **响应字段**（官方 schema，经 API 知识库镜像逐字段核对）：
  - `key.usage`（本 key 本计费周期已用 credits）、`key.limit`（**可为 `null`，表示无限制**）
  - `key.search_usage` / `key.extract_usage` / `key.crawl_usage`
  - `account.current_plan`、`account.plan_usage`、`account.plan_limit`、`account.paygo_usage`、`account.paygo_limit`
  - `account.search_usage` / `account.extract_usage` / `account.crawl_usage` / `account.map_usage` / `account.research_usage`
  - 来源：https://www.withone.ai/knowledge/tavily/conn_mod_def%3A%3AGJ7MfsKCcFU%3A%3A04VHkdg_TeOFvfzeINv9BA （One 平台的 Tavily API 知识条目，逐字段引用官方文档说明）
- **是否豁免 credit 消耗？未文档化。** 官方只文档化了它**有独立限流**（10/10min），未见任何「/usage 不消耗 credit / 不计入限额」的说明 → 应假定它**受限流约束**。
- 相关：官方还有 `/logs`（按请求日志，付费 plan）与 Enterprise 的 Organization Usage / Key Info 端点。 https://docs.tavily.com/llms.txt

### 3. ToS / AUP 中与「多 key、自动化」相关的原文

- **账号唯一性**（https://www.tavily.com/terms ，Last updated: May 4, 2026）：
  > 「Unless otherwise specifically specified under the Order Form, (X) each Order Form provides the Customer and its Users with access to a single Account only, and (Y) any additional Accounts may require the issuance of a separate Order Form and may incur additional charges.」
  > 「Customer (i) may not, and will ensure that Users do not, share Account information with any third party, (ii) will keep, and ensure that all Users keep, each Account and all Account information and credentials (including API security keys and any Agent Key) secure...」
- **Agent Key 条款**：通过 Agent Key 的一切访问「shall constitute Customer's access to and use of the Services」，客户承担全部责任。
- **AUP 禁止条款**（https://www.tavily.com/acceptable-use-policy ，第 2 节 Prohibited Uses）：
  > 「to interfere with, disrupt, degrade, impair, or attack the Services, the Tavily platform, or any network or system connected thereto, including through flooding, denial-of-service attacks, or the use of bots or automated scripts to generate excessive or abusive load on the Services」
- **未找到**：任何明确禁止「同一账号下创建多个 API key」的条款，也没有任何「自动化阈值」「risk control / 风控系统」「bot protection」的官方说明文字。

### 4. 429 / `Retry-After` / 432 / 433

- 429：官方 docs 原文「When you exceed the rate limit, the API returns a **429 Too Many Requests** response with a **`retry-after` header** indicating the number of seconds to wait before...」 https://docs.tavily.com/documentation/rate-limits
- 官方 Search 端点页示例错误体亦含 `429`（「Please reduce rate of requests.」）与 `432`（「This request exceeds your plan's set usage limit.」）https://docs.tavily.com/documentation/api-reference/endpoint/search
- **432 / 433 官方定义**（tavily-ai 官方 GitHub 组织仓库 README）https://github.com/tavily-ai/tavily-n8n-node
  | Code | 含义 | 处理 |
  |---|---|---|
  | 429 | Rate limit exceeded | Reduce request frequency or implement backoff |
  | 432 | Plan Limit Exceeded | Upgrade your plan via Tavily Dashboard |
  | 433 | Pay-As-You-Go Limit Exceeded | Increase limit via Tavily Dashboard |
  - 同一表格亦见于官方帮助中心：https://help.tavily.com/articles/8645538886-understanding-http-errors

---

## (b) 第三方 / 社区来源（非官方）

- **`/usage` 实测会返回 429**（最直接的证据）：
  - https://github.com/shaftoe/pi-tavily-tools/issues/10 —— 报告「Tavily Usage endpoint 429 rate limit errors during active sessions」。原文指出：其缓存冷却 `FETCH_COOLDOWN_MS = 30_000`（30 秒）「allows up to 20 requests per 10-minute window — **double Tavily's limit of 10**」，并给出实测响应头 `HTTP/2 429 Too Many Requests` / `retry-after: 60`。
  - 修复 PR：https://github.com/shaftoe/pi-tavily-tools/issues/13 —— 解析 `retry-after`，缺省回落 **300 秒（5 分钟）** 退避，429 时保留上次已知用量而非清空。
- **`/usage` = 10 次 / 10 分钟（dev 与 prod 相同）**：
  - https://www.scalekit.com/blog/tavily-mcp-vs-api （列出表格：Usage endpoint 10 per 10 min；并称限流「per key and per endpoint class」）
- **实测响应结构与非文档字段**：
  - https://cdn.jsdelivr.net/npm/tavily-mcp-multikey@1.1.0/API_VERIFICATION.md —— 实测 21 个 key；真实响应 `{"key":{"usage":232,"limit":null},"account":{"current_plan":"Researcher","plan_usage":232,"plan_limit":1000,"extract_usage":44,...}}`，指出 `extract_usage` 等字段**文档未提及但实际存在**；并记录 429 时改用 `/usage` 判定额度。该库还实现了「按 key 配额耗尽标记 + 定时恢复」。
- **多 key 轮换的工程实践与问题**：
  - https://github.com/AstrBotDevs/AstrBot/issues/8886 —— Tavily 多 key 轮询在 key 失效/额度用尽/被限流时**不会自动切换**下一个 key。
  - https://github.com/openclaw/openclaw/issues/59374 —— 请求支持多 Tavily key 自动故障转移（429/额度耗尽时轮换），**被 closed as not planned**。
  - https://glama.ai/mcp/servers/pzehrel/tavily-proxy-mcp —— 一个 Tavily 代理 MCP，说明「429 不一定代表月度额度耗尽」，会在语义不明时调用 `/usage` 判断。
- **`/usage` 可能较慢的超时处理**（弱证据）：
  - https://github.com/marsmay/UsageBoard/blob/main/Resources/BundledPlugins/tavily-usage-plugin.py —— 对 `/usage` 使用 `timeout=5`。
- **搜索端点 429 的社区报告**（非 `/usage`）：
  - https://github.com/assafelovic/gpt-researcher/issues/1047
  - https://github.com/PrefectHQ/ControlFlow/issues/396
- **不可靠来源（明确标注）**：
  - https://theneuralbase.com/tavily-api/learn/beginner/429-rate-limit 与 .../advanced/rate-limits-by-plan —— 声称「free tier 10 req/min」「free: 1 req/min」「1000 searches/month」等，与官方文档（dev 100 / prod 1000 RPM）**明显冲突**，且自称 AI 生成课程。**不要采信其数字。**

---

## (c) 无法确认的事项

1. `/usage` 是否豁免 credit 消耗 / 是否计入月度 credits —— 无官方说法。
2. 官方是否对「滥用/自动化」设有具体数值阈值，或存在「风控/风险控制」检测机制 —— 官方文档、ToS、AUP 均无。社区亦无「因频繁轮询 `/usage` 而被封号」的可核实报告（只有 429 限流报告）。
3. 是否存在「同一账号多 key」的官方限制 —— ToS 只限制「多 Account」，未限制「单 Account 多 key」；事实上官方博客明确支持「Create and manage multiple API keys, assigning unique names and **setting usage limits for each**」（https://www.tavily.com/blog/getting-started-with-the-tavily-search-api ），说明多 key 是被支持的产品能力。
4. `/usage` 是否常出现慢响应/超时 —— 仅有第三方使用 5 秒超时这一间接迹象，无公开报告。
5. free 与 paid 是否在 RPM 上有差异 —— 官方只按环境（dev/prod）区分。
6. `/research` 20 RPM 仅有第三方来源（https://parallel.ai/articles/tavily-vs-parallel-search ，https://www.scalekit.com/blog/tavily-mcp-vs-api），未在官方页面直接核实。

---

## 对轮询频率设计的直接推论

- 官方硬上限：**`/usage` 每 key 每 10 分钟 10 次** → 理论最短间隔 **60 秒**。
- 但第三方实测在 30 秒间隔下即触发 429，且 429 的 `retry-after` 实测为 60 秒；工程上建议 **≥ 60 秒，稳妥取 300 秒（5 分钟）**，并：
  1. 必须解析并遵守 `Retry-After`；
  2. 429 时进入退避窗口并沿用缓存值（勿清空）；
  3. 按 key 维度缓存与限流（限制是 per key 的）；
  4. 注意 `key.limit` 可能为 `null`（无限制），解析时不可假设为整数。

> 以上「推论/建议」部分属于基于已确认事实的工程判断，非 Tavily 官方说法。
