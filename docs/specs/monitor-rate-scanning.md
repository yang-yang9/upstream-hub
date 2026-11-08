# Spec：监控扫描 — 余额 / 倍率

> 周期性拉取所有启用监控的渠道的余额与模型倍率，检测变化并落库 / 触发通知。

## 背景

upstream-hub 的核心职责是盯着 NewAPI / Sub2API 上游站点：余额掉到阈值以下要告警，分组倍率变化要留痕并推送。扫描由 scheduler 的 cron 触发（balanceCron / rateCron），经 `monitor.Service` 对每个 `monitor_enabled` 的渠道执行；变化判定是纯逻辑（`monitor/changes.go`），落库走 storage 层（`rate_snapshots` UPSERT 返回旧值、`rate_change_logs`、`balance_snapshots`），通知走 notify.Dispatcher。前端以 `useDashboardSummary` / `useRateChanges` 拉取展示。

本 spec 覆盖：变化检测语义、低余额判定、UPSERT prev 契约、聚合查询语义、前端拉取契约。

## 行为规格

### AC-1: 倍率快照 UPSERT 返回旧值（变化检测的依赖契约）

- **Given** `rate_snapshots` 已有 (channel, model) 行，值为 (ratio=2, completion=3)
- **When** 以新值 (2.5, 3.5) UPSERT
- **Then** 返回非 nil 的 prev（值为**旧值** 2/3），当前行更新为新值；首次插入返回 nil prev 且 FirstSeenAt 取 LastSeenAt

### AC-2: 倍率变化检测（rateChangeDiff）

- **Given** 某渠道某分组存在 prev 快照
- **When** 新扫描的 ratio 或 completion_ratio 任一不同
- **Then** 生成一条 `RateChangeLog`（OldRatio / OldCompletionRatio 为**旧值**指针）+ `notify.RateChange` 投影；prev 为 nil（首见）也视为变化；双值完全相等 → 无变化、不写日志

### AC-3: 低余额判定与消息（balanceLowMessage）

- **Given** 渠道配置了 `balance_threshold > 0`
- **When** 扫描余额 `balance < threshold`（严格小于）
- **Then** 生成 balance_low 通知：Subject `[upstream-hub] <渠道名> 余额低于阈值`，Body `当前余额: <%.4f>，阈值: <%.4f>`；threshold ≤ 0（未配置）或 balance ≥ threshold 不触发

### AC-4: 余额快照与历史

- **Given** 每次余额扫描成功
- **When** 写入 `balance_snapshots`
- **Then** `BalanceHistory` 按 sampled_at 倒序返回；聚合 `AggregateBalanceTrend`（按天）/ `AggregateBalanceTrendHourly`（按小时）= 每 (channel, bucket) 取最后一条再按 bucket 求和，窗口外的行不参与

### AC-5: 前端拉取与展示

- **Given** Dashboard 页面挂载
- **When** `useApi` 请求 `/api/dashboard/summary` 等
- **Then** fetch 自动拼 `/api` 前缀；同 path + tick + bump 的并发请求去重共享一次 fetch；已完成响应缓存 800ms（TTL）；倍率变动渲染 `旧值 → 新值` 箭头、`±X.X%` 涨跌幅、old_ratio 为 null 显示 `—` 与 `新增`；KPI 的"今日倍率变动"按本地 0 点统计 `changed_at`

### AC-6: 监控日志

- **Given** 每次扫描（成功或失败）
- **When** 写入 `monitor_logs`
- **Then** 零值字段自动填（StartedAt / FinishedAt / DurationMS），显式值保留；`List` 倒序、支持 channelID 过滤

## 边界用例

| 边界 | 期望 | 测试 |
|------|------|------|
| 首见分组（prev=nil） | 视为变化（新增），OldRatio 指针为旧值/0 | `TestRateChangeDiff` |
| 双值相等 | 不写 rate_change_logs | `TestRateChangeDiff` |
| balance == threshold（正好等于） | 不告警（严格小于） | `TestBalanceLowMessage` |
| threshold = 0（未配置） | 不告警 | `TestBalanceLowMessage` |
| UPSERT 更新返回值 | 返回**旧值**而非新值 | `TestRatesUpsertPrevContract` |
| 同一桶内多条余额 | 取 sampled_at 最后一条 | `TestAggregateBalanceTrendDaily/Hourly` |
| 窗口外余额 | 不参与聚合 | 同上 |
| 空表聚合 | 返回 0 行不报错 | `TestAggregateBalanceTrendEmpty` |
| 前端缓存 TTL 800ms | 重挂载不重复 fetch | `queries.test.tsx` |
| DeleteBefore 语义 | 严格小于 cutoff，等于保留 | `TestMonitorLogsDeleteBefore` 等 |

## 非目标

- 不做按模型 / 分组的自定义监控规则引擎（roadmap 单列）。
- 不做扫描失败的重试 / 补偿——失败写 monitor_logs + last_error，等下个 cron 周期。
- 不预测余额趋势（消耗预测是独立 spec）。
- 前端不做轮询以外的实时推送（SSE 同步流不在本 spec 范围）。

## 关联代码

- 实现：`backend/internal/monitor/changes.go` — `rateChangeDiff` / `balanceLowMessage`
- 实现：`backend/internal/monitor/service.go` — `ScanAllBalances` / `ScanAllRates` / `RefreshBalance` / `RefreshRates`
- 实现：`backend/internal/storage/rates.go` — `Upsert` / `AppendChange` / `AppendBalance` / `BalanceHistory` / `AggregateBalanceTrend(Hourly)`
- 单元测试：`backend/internal/monitor/changes_test.go` — `TestRateChangeDiff` / `TestBalanceLowMessage`
- 集成测试（env 门控 `UPSTREAMHUB_TEST_DB=1`）：`backend/internal/storage/rates_test.go` / `monitor_logs_test.go`
- 前端：`frontend/lib/queries.ts`（`useApi` / 缓存）、`frontend/lib/format.ts`（`ratioArrow` / `ratioDelta`）、`frontend/lib/api.ts`（`apiFetch`）；测试 `frontend/lib/queries.test.tsx` / `format.test.ts` / `api.test.ts`、`frontend/components/monitor/kpi-row.test.tsx` / `multiplier-changes.test.tsx`

## 变更记录

- 2026-11-08 — 初稿（SDD 改造回填，AC 对应 Phase 3–7 测试）
