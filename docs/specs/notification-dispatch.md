# Spec：通知分发

> 事件（倍率变化 / 低余额 / 系统告警）按订阅关系扇出到启用的通知渠道，带去抖、合并与重试。

## 背景

监控扫描产生事件后，`notify.Dispatcher` 负责把消息发给所有**启用的**通知渠道（Bark / 钉钉 / 邮件 / Webhook）。渠道配置（含密钥）以密文存储，运行时解密并经 `Register` 注册的工厂构造 Notifier。分发有三层控制：订阅过滤（哪些渠道关心哪个上游）、冷却去抖（balance_low 不重复轰炸）、合并策略（同次扫描多个分组变动合并成一条）。发送结果写 `notification_logs`。

## 行为规格

### AC-1: 扇出与订阅过滤

- **Given** 存在启用渠道，其订阅列表为 `[]`（空 = 订阅一切）
- **When** 任一事件 Dispatch
- **Then** 该渠道收到消息；订阅了**其它**上游（channel_id 不匹配）的渠道不收到

- **Given** 渠道订阅 mode=groups（只订某些分组）
- **When** 非 rate 事件（如 balance_low）Dispatch
- **Then** 仍收到（分组过滤只作用于 rate 变化条目；此为既有行为，前端/用户以此为准）

### AC-2: rate 事件的订阅切片

- **Given** 渠道订阅上游 A 的 groups=["g1"]
- **When** `DispatchRateBatch` 携带 g1 / g2 两条变化
- **Then** 该渠道收到的合并消息只含 g1；rate_changed 事件不参与 balance_low 冷却

### AC-3: 低余额冷却去抖（suppress）

- **Given** balance_low 事件 + cooldown > 0
- **When** `TryClaimCooldown(channel, event, cooldown)` 返回 false（冷却中）
- **Then** 不发送；返回 true 或 cooldown 存储**出错**时照发（fail-open）；channel_id=0（系统消息）、非 balance_low 事件、cooldown=0（未配置）均绕过冷却

### AC-4: 冷却原子性（并发唯一赢家）

- **Given** 同一 (channel_id, event) 无冷却记录
- **When** N 个 goroutine 并发 TryClaimCooldown
- **Then** 恰好 1 个返回 true（PostgreSQL `ON CONFLICT ... WHERE` 原子 UPSERT）

### AC-5: 批量合并与最小变动过滤

- **Given** Policy `BatchRateChanges=true`
- **When** 同次扫描产生多条变化
- **Then** 合并为 1 条消息（标题含"N 个分组变动"）；`MinChangePct` > 0 时涨跌幅小于阈值的条目被滤掉但仍计入被过滤数；**首见（OldRatio=0）恒不被过滤**；`BatchRateChanges=false` 时逐条发送

### AC-6: 重试与退避

- **Given** 发送失败且 `SendMaxAttempts` > 1
- **When** Notifier.Send 报错
- **Then** 指数退避重试（1s/2s/4s… 上限 30s）；ctx 取消立即终止返回 context.Canceled；`SendMaxAttempts` ≤ 0 归一化为 1

### AC-7: 部分失败隔离

- **Given** 多渠道扇出，其中一个渠道始终失败
- **Then** 其余渠道照发；每个渠道各写一条 notification_logs（成功 / 失败）；Dispatch 返回 errors.Join 的聚合错误（含失败渠道名）

## 边界用例

| 边界 | 期望 | 测试 |
|------|------|------|
| 无启用渠道 | Dispatch 直接返回，不报错 | `TestNewDispatcherWithStoreEmptyFanout` |
| store 出错（ListEnabledChannels 失败） | Dispatch 直接返回错误 | `TestDispatchRateBatch` 尾段 |
| nil channel / 空 changes 守卫 | 直接返回，不触碰 store | `TestDispatchRateBatch` |
| 订阅 JSON 非法 | ParseSubscriptions 报错 | `TestParseSubscriptions` |
| MinChangePct 与首见 | OldRatio=0 恒过 | `TestChangePctAbove` |
| cooldown 存储 fail-open | 出错照样发 | `TestDispatchSuppress` |
| Message.Extra 不可 == 比较 | 测试用字段断言 | `TestBuildBatchMessageEmpty` |
| DeleteLogsBefore | 严格小于 cutoff | `TestNotificationsLogs` |

## 非目标

- 不做通知发送的持久队列 / 死信（失败只重试 + 写日志）。
- 不做按用户的个性化通知（面向管理员单租户）。
- 不做 Bark / 钉钉等具体渠道的协议测试（各渠道 Send 是薄封装，出错路径由 sendWithRetry 覆盖）。
- 不新增通知渠道类型（新渠道 = 新增 notifier 文件 + Register，不触碰 Dispatcher）。

## 关联代码

- 实现：`backend/internal/notify/dispatcher.go` — `Dispatch` / `DispatchRateBatch` / `suppress` / `fanout` / `sendWithRetry` / `backoffDelay` / `NotifierStore` 接口
- 实现：`backend/internal/notify/policy.go` — `ChangePctAbove` / `BuildBatchMessage`；`subscription.go` — `ParseSubscriptions` / `Matches` / `AnyMatch`
- 实现：`backend/internal/storage/notifications.go` — `TryClaimCooldown` / `ListEnabledChannels` / `AppendLog`
- 单元测试：`backend/internal/notify/dispatcher_test.go`（fake store / notifier / cooldown 缝隙）、`policy_test.go`、`subscription_test.go`
- 集成测试（env 门控）：`backend/internal/storage/notifications_test.go`
- 注入缝隙：`NewDispatcherWithStore(store NotifierStore, ...)` 供单测用；生产构造器 `NewDispatcher` / `NewDispatcherWithCooldown` 签名不变

## 变更记录

- 2026-11-08 — 初稿（SDD 改造回填，AC 对应 Phase 3–6 测试）
