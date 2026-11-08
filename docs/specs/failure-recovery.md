# Spec：失败风暴防刷屏与恢复通知

> 站点挂掉后，每个渠道每轮扫描都会推一遍 `login_failed` / `monitor_failed`，造成通知风暴；且只有触发没有恢复，用户分不清"还在异常"和"已经好了"。本能力做三层：失败事件冷却（节流重发）、连续 N 次失败才告警（滤掉瞬时抖动）、恢复通知（成功后发"已恢复"）。

## 背景

现状（勘察确认）：

- 失败事件产生点在 `monitor.Service` 的 `RefreshBalance` / `RefreshRates`：`prepare` 失败 → `login_failed`；`GetBalance` / `GetRates` 失败 → `monitor_failed`。两类事件**每次失败必发**，`cooldownForEvent` 不覆盖它们。
- 冷却基建已就绪：`notification_cooldowns` 表按 (channel, event) 原子 UPSERT，跨重启；扩展只需在 `cooldownForEvent` 加映射。
- 恢复信号已存在但无人消费：`channels.last_error` 失败时写、成功时清；`monitor_logs` 记录每次成败。
- **关键语义坑**：`monitor_failed` 事件被 balance（15 分钟一轮）和 rates（30 分钟一轮）两个 job 共用。若连续失败计数按 event 维度，balance 的成功会不断清掉 rates 攒的失败计数，rates 类持续故障永远达不到阈值。因此**计数按 job 维度**（login / balance / rates），告警时再映射回事件。

## 行为规格

### AC-1: 失败事件冷却（dispatcher 层，节流重发）

- **Given** `login_failed` / `monitor_failed` 事件 + `FailureCooldown` > 0
- **When** Dispatch
- **Then** 同渠道同事件在冷却窗口内不重复发送；窗口过后首次失败重发（"还在异常"提醒）；冷却存储出错照发（fail-open）、channel_id=0 绕过、`FailureCooldown=0` 绕过——与 balance_low 同语义
- **Given** `balance_low` / `balance_forecast_low`
- **When** `cooldownForEvent`
- **Then** 仍映射各自冷却，互不掺和（既有行为不变）

### AC-2: 连续失败计数（按 job 维度）

- **Given** 某渠道某 job（login / balance / rates）连续失败
- **When** 每次失败 `RecordFailure(channelID, job)`
- **Then** 计数原子 +1，`first_failed_at` 首次写入后不变，`last_failed_at` 刷新；并发调用不丢计数（PostgreSQL `ON CONFLICT DO UPDATE` 原子递增）

### AC-3: 阈值告警（连续 N 次才告）

- **Given** `FailureThreshold`（默认 3，<=1 视为每次失败即告警）
- **When** 本次失败后计数 >= 阈值
- **Then** 走既有 `notifyError` 派发失败告警（标题格式不变，正文附"连续失败 N 次"）；计数 < 阈值时不派发（瞬时抖动静默）
- 重发节流由 AC-1 的冷却负责，monitor 层不做"已告警过"标记

### AC-4: 恢复通知

- **Given** 某渠道某 job 存在失败计数（本周期失败过）且本次扫描成功
- **When** 对应 job 的成功路径
- **Then** 取走并清除该 streak；若**计数曾达到过阈值**（即派发过失败告警），派发恢复事件——`login_failed` 对应 `login_recovered`、`monitor_failed` 对应 `monitor_recovered`——标题"[upstream-hub] {渠道名} 已恢复（{登录/余额采集/倍率采集}）"，正文含此前连续失败次数；计数未达阈值（静默抖动）→ 静默清除，不发恢复
- 恢复事件不参与冷却（每失败周期天然至多一条）
- **Given** `prepare` 成功（登录/会话正常）
- **When** 任一 job 扫描开始
- **Then** 视为 login job 恢复点：取走 login streak 并按上述规则决定是否发 `login_recovered`

### AC-5: job → 事件映射

| job | 失败事件（既有） | 恢复事件（新增） | 恢复点 |
|-----|------------------|------------------|--------|
| login | login_failed | login_recovered | prepare 成功（任一 job） |
| balance | monitor_failed | monitor_recovered | GetBalance 成功 |
| rates | monitor_failed | monitor_recovered | GetRates 成功 |

- `monitor_failed` 的冷却按 (channel, event) 共享：balance 与 rates 的告警互相占用同一冷却窗口，属可接受的粗粒度（都是"该渠道采集异常"信号）

### AC-6: 配置

- `notifications.failureThreshold`（默认 3）→ monitor 层阈值
- `notifications.failureCooldownMinutes`（默认 60）→ notify Policy
- 环境变量 `UPSTREAMHUB_NOTIFICATIONS_FAILURE_THRESHOLD` / `UPSTREAMHUB_NOTIFICATIONS_FAILURE_COOLDOWN_MINUTES`（camelCase key 显式 BindEnv，同 forecast 的坑）

## 边界用例

| 边界 | 期望 | 测试 |
|------|------|------|
| `FailureThreshold <= 1` | 每次失败即告警（退化为纯冷却模式） | `TestShouldAlertFailure` 表驱动 |
| 计数未达阈值的抖动（失败 2 次即成功） | 无告警、恢复也静默 | `TestFailureTrackerOnSuccess` |
| 恢复时 streak 不存在（一直健康） | 不发恢复，不报错 | 同上 |
| 阈值刚达成的下一次失败即成功 | 告警已发、恢复照发 | 纯函数表驱动 |
| 冷却存储出错 | 照发（fail-open，沿用既有） | `TestDispatchSuppress` 扩展用例 |
| 手动触发的同步失败 | 同样计数（用户连点重试失败 3 次也会告警；可接受，UI 上本就可见错误） | 不单测，行为说明 |
| streak 表并发递增 | 原子，不丢计数 | env 门控集成测试 |
| 服务重启 | streak 持久化在 PostgreSQL，重启后计数延续（漏报 0 误报 0） | 集成测试顺序两次 Open 不便模拟，由表持久化语义保证 |

## 非目标

- 不做失败退避（连续失败后拉长扫描间隔）——那是 roadmap P3-13，独立 spec。
- 不做 balance_low / rate_changed 的恢复通知（P0-3 告警恢复的其余部分，独立 spec）。
- 不做 per-channel 独立冷却配置（P1-6）。
- 不做 captcha_failed 的派发（该常量目前从未被派发，属死代码，不在本 spec 处理）。
- 恢复通知不携带故障根因（错误详情在失败告警与 UI 的 last_error 里）。

## 关联代码

规格对应的实现与测试位置（实现后回填）：

- 纯逻辑：`backend/internal/monitor/failure.go` — `FailureTracker`（`OnFailure` / `OnSuccess`）+ `shouldAlertFailure` / `recoveryEligible` 纯函数 + job 标签映射
- 存储：`backend/internal/storage/model.go` — `FailureStreak` 模型（表 `failure_streaks`）；`backend/internal/storage/failures.go` — `RecordFailure` / `TakeStreak`
- 事件常量：`backend/internal/storage/model.go` — `EventLoginRecovered` / `EventMonitorRecovered`
- dispatcher：`backend/internal/notify/policy.go` — `Policy.FailureCooldown`；`backend/internal/notify/dispatcher.go` — `cooldownForEvent` 扩展
- 配置：`backend/internal/config/config.go` — `NotificationsConfig.FailureThreshold` / `FailureCooldownMinutes` + BindEnv
- 接线：`backend/internal/monitor/service.go` — `notifyFailure` / `notifyRecovery`（`RefreshBalance` / `RefreshRates` 的登录、采集失败与成功路径调用）；`backend/cmd/server/main.go` — `storage.NewFailures` + `Policy.FailureCooldown` + `monitor.NewFailureTracker` 组装
- 配置测试：`backend/internal/config/config_test.go` — 默认值与 env 覆盖
- 前端：`frontend/lib/api-types.ts` — `NotificationEvent` 扩展 `login_recovered` / `monitor_recovered`；`frontend/components/monitor/bottom-panels.tsx` — 恢复事件图标（ShieldCheck / text-success）
- 单元测试：`backend/internal/monitor/failure_test.go`（fake FailureStreakStore）、`backend/internal/notify/dispatcher_test.go`（`TestCooldownForEventFailureEvents` / `TestDispatchSuppressFailureEvents`）
- 集成测试（env 门控 `UPSTREAMHUB_TEST_DB=1`）：`backend/internal/storage/failures_test.go` — 递增原子性、TakeStreak 语义、并发递增；`backend/internal/storage/storagetest/harness.go` — `failure_streaks` 纳入清表清单

## 变更记录

- 2026-11-08 — 初稿（P1-4，待评审）
- 2026-11-08 — 实现完成（P1-4）：后端单测全绿；`UPSTREAMHUB_TEST_DB=1` 集成测试交由 CI postgres:16 跑；关联代码已回填
