# Spec：余额消耗速率与到期预测

> 基于 `balance_snapshots` 时间序列做线性回归，计算渠道日均消耗、预测"N 天后余额耗尽"，并在临近耗尽时提前告警。

## 背景

当前低余额告警是**静态阈值**（`channel.monitor_threshold`）：阈值靠人猜，猜大了天天烦、猜小了来不及。而每次余额扫描（默认 15 分钟一次）已经在 `balance_snapshots` 落一条快照，90 天保留——消耗**速度**是现成可测的。对窗口内快照做最小二乘线性回归得到日均消耗斜率，外推耗尽时点，比阈值更贴近真实使用节奏。

本能力交付三层：纯函数回归计算（`monitor/forecast.go`）、存储窗口查询（`ListBalanceSince`）、REST 展示 + 扫描尾部的预测告警事件。

## 行为规格

### AC-1: 线性回归计算（纯函数）

- **Given** 某渠道窗口内的余额快照序列（≥2 个、时间递增、余额线性下降）
- **When** `forecastBurn(samples, now, windowDays)`
- **Then** 返回 `BurnForecast{DailyBurn, DaysLeft, DepletedAt, Samples, WindowDays}`：`DailyBurn` 为正数（日均消耗），`DaysLeft = 最新样本余额 / DailyBurn`，`DepletedAt = 最新样本时间 + DaysLeft×24h`，`Samples` 为实际参与回归的点数

### AC-2: 窗口过滤与最小样本

- **Given** 快照序列含早于 `now - windowDays×24h` 的样本，或仅 1 个样本在窗口内
- **When** `forecastBurn`
- **Then** 窗口外的样本被剔除（不参与回归）；剔除后不足 2 个样本 → 返回 `ok=false`，不产出预测

### AC-3: 非消耗状态不预测

- **Given** 窗口内余额斜率 ≥ 0（持平 / 上升，含刚充值的情况）
- **When** `forecastBurn`
- **Then** 返回 `ok=false`（`DailyBurn` 非正 = 不会耗尽）；下游不展示预测、不告警

### AC-4: 窗口查询（storage）

- **Given** `balance_snapshots` 存在窗口内外多个渠道的样本
- **When** `ListBalanceSince(channelID, since)`
- **Then** 只返回该渠道 `sampled_at >= since` 的行（升序）；无匹配返回空切片；与 `BalanceHistory` 的倒序 limit 语义互不影响

### AC-5: 预测查询 API

- **Given** 已认证请求 `GET /api/channels/:id/forecast`
- **When** 渠道存在且有窗口内足够样本
- **Then** 返回 `{daily_burn, days_left, depleted_at, samples, window_days, depleting}`；`depleting=false` 时数值字段为零值、`daily_burn` 为 0；渠道不存在 → 404。回归在服务端基于当前时间执行（结果非持久化）

### AC-6: 临近耗尽告警

- **Given** 余额扫描成功、追加快照后计算的预测满足 `ok=true` 且 `DaysLeft <= forecastAlertDays`（默认 7）
- **When** `RefreshBalance` 尾部
- **Then** 以新事件 `balance_forecast_low` Dispatch 一条消息（标题含渠道名与"预计 N 天后耗尽"，正文含当前余额 / 日均消耗 / 预计耗尽日期）；`forecastAlertDays <= 0` 时该告警整体关闭
- **Given** 同渠道该事件在 `forecastCooldownMinutes`（默认 720）冷却期内
- **When** 再次扫描再次命中阈值
- **Then** 不重复发送（复用冷却表，与 balance_low 相同的 fail-open 语义：冷却存储出错照发；非本事件不参与本冷却）

### AC-7: 前端渠道卡片展示

- **Given** 渠道卡片渲染且该渠道 `depleting=true`
- **When** 预测数据到达
- **Then** 卡片显示"日均消耗 $X · 预计 N 天后耗尽"（N <1 时显示小时；耗尽时点在过去显示"已耗尽"）；`depleting=false` 不显示预测行；请求失败静默（不阻塞卡片主体信息）

## 边界用例

| 边界 | 期望 | 测试 |
|------|------|------|
| 窗口内仅 1 个样本 | ok=false | `TestForecastBurn` 表驱动 |
| 斜率恰好为 0 | ok=false（非消耗） | 同上 |
| 最新余额 ≤ 0 且在下降 | DaysLeft ≤ 0，预测照常产出（前端显示"已耗尽"） | 同上 |
| 充值跳变混入窗口 | 回归被拉高，可能 ok=false（漏报可接受，见非目标） | 同上 |
| 时区 | 回归用绝对时间戳，`now` 由调用方注入（UTC） | 同上 |
| 冷却存储出错 | 照发（fail-open） | `notify` dispatcher 测试 |
| `forecastAlertDays=0` | 告警关闭，不 claim 冷却 | monitor 服务测试 |
| API 对不存在渠道 | 404 | `api` 层测试 |
| 前端 fetch 失败 | 卡片主体照常渲染 | `channel-cards` 组件测试 |

## 非目标

- **不剔除充值跳变**：一次大额充值会把斜率拉平甚至转正，导致漏报而非误报；接受此偏差，后续如需可立独立 spec（分段回归 / 跳变检测）。
- 不做预测持久化（每次现算，快照即真相）。
- 不做跨渠道汇总预测（总余额趋势已有 `AggregateBalanceTrend`；面板只关心单渠道耗尽）。
- 不做非线性 / 加权回归（15 分钟粒度 672 点的最小二乘已足够抗噪）。
- 预测数值不进通知 Extra 字段之外的任何新表。

## 关联代码

规格对应的实现与测试位置（实现后回填）：

- 纯函数：`backend/internal/monitor/forecast.go` — `forecastBurn` / `BurnForecast` / `balanceForecastMessage`
- 存储：`backend/internal/storage/rates.go` — `ListBalanceSince`
- 事件常量：`backend/internal/storage/model.go` — `EventBalanceForecast`
- 配置：`backend/internal/config/config.go` — 顶层 `ForecastConfig`（`forecast:` 节，环境变量 `UPSTREAMHUB_FORECAST_WINDOW_DAYS / UPSTREAMHUB_FORECAST_ALERT_DAYS / UPSTREAMHUB_FORECAST_COOLDOWN_MINUTES`）
- 接线：`backend/internal/monitor/service.go` — `Service.Forecast` / `dispatchForecast`（`RefreshBalance` 尾部）；`backend/internal/notify/dispatcher.go` — `cooldownForEvent` 事件→冷却映射；`backend/internal/api/channels.go` — `GET /channels/:id/forecast`；`backend/cmd/server/main.go` — 配置注入
- 前端：`frontend/lib/queries.ts` — `useChannelForecast`；`frontend/lib/api-types.ts` — `ChannelForecast`；`frontend/components/monitor/channel-cards.tsx` — `ForecastRow`；`frontend/lib/format.ts` — `depletionText`；`frontend/components/monitor/bottom-panels.tsx` — `balance_forecast_low` 事件图标
- 单元测试：`backend/internal/monitor/forecast_test.go`（表驱动）、`backend/internal/notify/dispatcher_test.go`（新事件冷却独立性）
- 集成测试（env 门控 `UPSTREAMHUB_TEST_DB=1`）：`backend/internal/storage/rates_test.go` — `ListBalanceSince` 窗口语义；`backend/internal/api/forecast_test.go` — forecast 端点端到端
- 前端测试：`frontend/components/monitor/channel-cards.test.tsx`、`frontend/lib/format.test.ts` — `depletionText`

## 变更记录

- 2026-11-08 — 初稿（P0-1，待评审）
- 2026-11-08 — 实现落地：配置从 `MonitorConfig`/`NotificationsConfig` 分散字段改为独立顶层 `ForecastConfig`（`forecast:` 节），便于一处管理三个参数；回填关联代码与测试位置。
