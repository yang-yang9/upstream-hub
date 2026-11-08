# /metrics 暴露（Prometheus text format）

> 状态：已实现。AC 与测试对应关系见"关联代码"。

## 背景

upstream-hub 的可观测数据（余额、扫描成败、扫描耗时）全部只在自己的 PostgreSQL 和面板 UI 里。对已有 Prometheus / Grafana / Alertmanager 基建的用户，这些数据是接入断点：想看"渠道扫描延迟趋势""哪个渠道最近老失败"必须再开一个面板，而不是在既有 Grafana 里加一个 dashboard。

数据其实都在库：`channels`（LastBalance / MonitorEnabled）、`monitor_logs`（每轮扫描的 Success / DurationMS / StartedAt）。本 spec 只做**暴露**：新增一个 Prometheus text format 的 `/metrics` 端点，即时查库渲染。

设计决策（已与用户确认）：

- **零依赖手写** text format，不引 `prometheus/client_golang`。指标集固定且小（gauge + counter），渲染逻辑抽纯函数表驱动单测。
- **认证 = 可选 Bearer token**：env `UPSTREAMHUB_METRICS_TOKEN` 不设 → 公开（内网默认，行为同 `/healthz`）；设了 → 要求 `Authorization: Bearer <token>`。Prometheus 抓取侧一行 `bearer_token` 配置即可对接。
- **数据来源 = 每次 scrape 即时查库**：scrape 间隔通常 ≥15s，两条轻查询无压力；数据始终新鲜，进程重启不丢业务指标（仅进程内 scrape 计数器清零，属可接受语义）。

## 行为规格

### AC-1: 端点输出 Prometheus text format

- **Given** 服务已启动（无论是否配置 token、库中是否有数据）
- **When** `GET /metrics` 通过认证
- **Then** 返回 `200`，`Content-Type: text/plain; version=0.0.4; charset=utf-8`，body 为合法 Prometheus 文本格式（每个指标族先 `# HELP` 再 `# TYPE`，数值行 `指标名{标签} 值`）

### AC-2: 渠道级 gauge 指标

- **Given** 渠道 alpha（type=newapi，MonitorEnabled=true，LastBalance=87.5）
- **When** 抓取 `/metrics`
- **Then** 输出包含（完整指标集见下方"指标清单"）：

```
upstreamhub_channel_balance{name="alpha",type="newapi"} 87.5
upstreamhub_channel_monitor_enabled{name="alpha"} 1
upstreamhub_channels_total 1
```

### AC-3: 每渠道每 job 的最新扫描指标

- **Given** alpha 的 balance job 最近一条 monitor_log 为 Success=true、DurationMS=1234、StartedAt=Unix 秒 1731024000
- **When** 抓取 `/metrics`
- **Then** 输出三个 series，标签 `{name="alpha",job="balance"}`：

```
upstreamhub_channel_last_scan_success{name="alpha",job="balance"} 1
upstreamhub_channel_last_scan_duration_ms{name="alpha",job="balance"} 1234
upstreamhub_channel_last_scan_started_at{name="alpha",job="balance"} 1731024000
```

（每 (channel, job) 取 `started_at` 最新的一条；软删除渠道不出现。）

### AC-4: 进程级 counter 指标

- **Given** `/metrics` 已被抓取 2 次成功、1 次因 DB 故障失败
- **When** 再次抓取成功
- **Then** 输出 `upstreamhub_metrics_scrapes_total 2` 与 `upstreamhub_metrics_scrape_errors_total 1`（计数器在成功渲染前自增，本次请求计入后为 2）；另有恒为 1 的 `upstreamhub_up` 和 `upstreamhub_build_info{version="...",commit="..."} 1`

### AC-5: Bearer token 认证

- **Given** 未配置 `UPSTREAMHUB_METRICS_TOKEN`
- **When** 任意请求（无 Authorization 头）
- **Then** `200` 正常输出（公开，同 `/healthz`）
- **Given** 配置了 `UPSTREAMHUB_METRICS_TOKEN=secret`
- **When** 无 Authorization 头 / 错误 token / 格式错误的头
- **Then** `401`，不触发 DB 查询、不计数
- **When** `Authorization: Bearer secret`（scheme 大小写不敏感，token 区分大小写，恒定时间比较）
- **Then** `200` 正常输出

### AC-6: DB 查询失败返回 500

- **Given** channels 或 monitor_logs 查询返回错误
- **When** 抓取 `/metrics`
- **Then** `500`，`scrape_errors_total` 自增（下次成功抓取可见）

### AC-7: SPA fallback 不吞 /metrics

- **Given** 嵌入前端产物的部署
- **When** 请求一个未注册的路径
- **Then** NoRoute guard 对 `/metrics` 返回 404 而非回落 index.html（防御性，与 `/healthz` 同列）

## 指标清单

| 指标 | 类型 | 标签 | 来源 |
|---|---|---|---|
| `upstreamhub_up` | gauge | — | 恒 1（能响应即 up） |
| `upstreamhub_build_info` | gauge | version, commit | 编译注入 |
| `upstreamhub_channels_total` | gauge | — | channels 表行数 |
| `upstreamhub_channel_balance` | gauge | name, type | channels.LastBalance |
| `upstreamhub_channel_monitor_enabled` | gauge | name | channels.MonitorEnabled（1/0） |
| `upstreamhub_channel_last_scan_success` | gauge | name, job | monitor_logs 最新一条 Success（1/0） |
| `upstreamhub_channel_last_scan_duration_ms` | gauge | name, job | 同上 DurationMS |
| `upstreamhub_channel_last_scan_started_at` | gauge | name, job | 同上 StartedAt 的 Unix 秒 |
| `upstreamhub_metrics_scrapes_total` | counter | — | 进程内存（成功渲染次数） |
| `upstreamhub_metrics_scrape_errors_total` | counter | — | 进程内存（DB 失败次数） |

数值格式用 `strconv.FormatFloat(v, 'f', -1, 64)`（`'g'` 会把 Unix 时间戳渲染成 `1.731024e+09`，`'f'` 在本指标集的量级下最可读，且同为 Prometheus 合法语法）；渠道系列按 name 排序、job 系列按 (name, job) 排序，保证输出确定性。

## 边界用例

- **无渠道**：只输出进程级指标，`upstreamhub_channels_total 0`，无任何渠道系列（AC-2/3 不产生行）
- **LastBalance 为 nil**（从未采集成功）：跳过该渠道的 `channel_balance` 系列，其余照常
- **某渠道无 monitor_logs**：跳过该渠道的所有 `last_scan_*` 系列
- **标签值转义**：渠道名含 `\`、`"`、换行时转义为 `\\`、`\"`、`\n`（text format 规定），纯函数表驱动测试
- **token 前缀**：`bearer secret`（小写）通过；`Bearer  secret`（多空格）、`Token secret`、裸 `secret` 都 401
- **成功渲染的计数时点**：`scrapes_total` 在渲染前自增，因此首抓返回 1
- **软删除**：`List()` 天然排除 DeletedAt 非空行，集成测试显式断言

## 非目标

- **不做 Histogram / Summary**（扫描耗时的分布、分位数）——只导出"最近一次"；历史统计是 Prometheus 侧 rate/avg 的事
- **不做 OpenMetrics / protobuf 格式**、不做 gzip 协商
- **不做面板 UI**（前端零改动）
- **不做 per-path / per-label 的细粒度鉴权**——token 是端点级的
- **不落库任何抓取记录**

## 关联代码

- 实现：`backend/internal/metrics/render.go` — `Render(Snapshot) string`、`escapeLabelValue`、`labels` / `family` / `formatValue`（纯函数）、`CheckBearer`（恒定时间比较）
- 实现：`backend/internal/api/metrics.go` — `registerMetrics` handler（认证、查库、atomic 计数）
- 实现：`backend/internal/storage/monitor_logs.go` — `LatestPerChannel()`（DISTINCT ON，PostgreSQL 特有）
- 实现：`backend/internal/config/config.go` — `server.metricsToken`（`UPSTREAMHUB_METRICS_TOKEN` 显式 BindEnv）+ `backend/cmd/server/main.go` Deps 接线
- 实现：`backend/internal/api/api.go` — 挂 `/metrics` 路由 + `reservedPath`（NoRoute guard 含 `/metrics`）
- 单元测试：`backend/internal/metrics/render_test.go` — `TestCheckBearer`（AC-5 表驱动）、`TestEscapeLabelValue`（转义边界）、`TestRender`（AC-1/2 黄金输出 + 排序）、`TestRenderEmpty` / `TestRenderEscapesLabelValues` / `TestRenderSkipsOrphanLogs`（边界）
- 单元测试：`backend/internal/config/config_test.go` — `TestMetricsTokenFromEnv`（AC-5 env 覆盖与默认空）
- 单元测试：`backend/internal/api/api_test.go` — `TestReservedPath`（AC-7）
- 集成测试（env 门控 `UPSTREAMHUB_TEST_DB=1`）：`backend/internal/api/metrics_test.go` — `TestMetricsHappyPath`（AC-1/2/3/4）、`TestMetricsAuth`（AC-5 端到端 + 401 不计数）、`TestMetricsDBError`（AC-6）、`TestMetricsSoftDeletedChannel` / `TestMetricsEmptyDatabase`（边界）
- 集成测试（env 门控）：`backend/internal/storage/monitor_logs_test.go` — `TestMonitorLogsLatestPerChannel`（AC-3 DISTINCT ON 语义）
- 文档：`docs/deployment.md` — env 变量表补 `UPSTREAMHUB_METRICS_TOKEN`

## 变更记录

- 2026-11-08 — 初稿
- 2026-11-08 — 数值格式 `'g'` → `'f'`（Red 阶段发现时间戳会被渲染成科学计数法）
