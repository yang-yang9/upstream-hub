# 静默时段与 per-channel 冷却覆盖

> 状态：已实现（2026-11-09）。测试见各关联文件旁注。

## 背景

通知去抖目前只有一层：全局 env 配的冷却时长（`BalanceLowCooldown` / `ForecastCooldown` / `FailureCooldown`），对所有渠道一刀切（roadmap P1-6）。两个缺口：

1. **无静默时段**：通知 7×24 即时推送，凌晨三点余额低了 Telegram 立刻响。
2. **冷却时长全局一份**：想给重要渠道配 10 分钟冷却、次要渠道配 6 小时，只能改全局 env。

本 spec 补齐这两层。设计决策（已与用户确认）：

- **静默时段 = 弱版本**：窗口内直接不推送（不排队、不汇总）。事件本身照常写业务表（`rate_change_logs` / failure streaks / `monitor_logs`），不丢数据。汇总冲刷留给 P1-7 日报（共享「攒批 + 定时发」基建）。
- **默认关闭**：不配置 env = 现状 7×24 照发，零行为变化。
- **窗口内全部事件静默**（含 rate_changed 批量与恢复事件），语义最简。
- **per-channel 覆盖 = 按事件 JSON**：`channels.notify_cooldowns` 存 `{"balance_low":10,...}`，每类事件可单独覆盖，未写的键回落全局默认。
- 静默时段是 env 配置（同其余 Policy）；per-channel 覆盖落 channels 表，渠道表单可编辑。

## 行为规格

### AC-1: 静默时段默认关闭

- **Given** 未配置 `UPSTREAMHUB_NOTIFICATIONS_QUIETHOURS`
- **When** 任意事件触发通知
- **Then** 行为与现状完全一致（即时推送、冷却照常判定）

### AC-2: 静默时段配置与解析

- **Given** `UPSTREAMHUB_NOTIFICATIONS_QUIETHOURS=23:00-08:00`
- **When** 服务启动
- **Then** 配置解析成功；23:00:00 起到 07:59:59 的时间点在窗口内，08:00:00 起不在（`[start, end)`，start 含 end 不含；跨午夜窗口 = `t >= start OR t < end`）
- **Given** 值为 `8:00-23:00`（非 `HH:MM`）、`25:00-06:00`、`23:00-23:00`（start==end）等非法格式
- **When** 服务启动
- **Then** 配置加载报错，服务拒绝启动
- 时区：服务器本地时间（`time.Now()` 不做 UTC 转换）

### AC-3: 窗口内全部通知不推送、不占冷却名额

- **Given** 当前时间在静默窗口内
- **When** 任一事件走 `Dispatch`（含 balance_low / forecast / login_failed / monitor_failed / 恢复事件）或 `DispatchRateBatch`
- **Then** 不查询通知渠道列表、不发送、**不 claim 冷却名额**（保证出窗后条件仍成立的第一次触发照常发送）；写 debug 日志；`DispatchRateBatch` 的 MinChangePct 过滤 / 订阅切片 / rate_change_logs 落库行为不变（发生在 Dispatcher 之前）
- **Given** 当前时间在窗口内
- **When** 用户点通知渠道的「测试发送」
- **Then** 照常发送（`Send` 是显式用户动作，不经 Policy；钉住既有行为）

### AC-4: per-channel 冷却覆盖的存储与校验

- **Given** 渠道编辑提交 `notify_cooldowns = {"balance_low":10,"balance_forecast_low":30,"login_failed":5,"monitor_failed":5}`
- **When** 创建 / 更新渠道
- **Then** 落库到 `channels.notify_cooldowns`（文本列），GET / list 原样返回
- **Given** 提交的 JSON 非法（语法错误 / 键不是 4 类冷却事件之一 / 值为负数或非整数）
- **When** 创建 / 更新渠道
- **Then** `400`，渠道不落库
- **Given** 提交空串 / `{}` / 省略该字段
- **When** 创建 / 更新渠道
- **Then** 成功，等价于「不覆盖，用全局默认」

### AC-5: Dispatcher 按覆盖值判定冷却

- **Given** 渠道 alpha 配了 `{"balance_low":10}`，全局 `BalanceLowCooldown=60min`
- **When** alpha 的 balance_low 通知触发
- **Then** 冷却窗口用 10 分钟（覆盖生效）；beta 未配覆盖，继续用 60 分钟
- **Given** 渠道 alpha 配了 `{"login_failed":0}`
- **When** alpha 的 login_failed 通知触发
- **Then** 该事件不冷却（0 = 关闭冷却，沿用 `TryClaimCooldown` 的 `cooldown<=0` 短路语义），每次失败风暴告警都发
- **Given** 覆盖只对参与冷却的 4 类事件生效；rate_changed 等不参与冷却的事件不受影响（控制量靠合并 / 阈值）
- 覆盖经 Message 携带进 Dispatcher；DB 里若存在非法 JSON（绕过 API 写入），运行时解析失败 fail-open 回落全局默认并 warn

### AC-6: 渠道表单可编辑覆盖

- **Given** 渠道编辑弹窗
- **When** 展开「通知冷却覆盖」小节
- **Then** 4 个分钟数字输入框（余额低告警 / 余额耗尽预测 / 登录失败 / 监控失败），留空 = 跟随全局；已有覆盖值回填；提交时只序列化非空项为 `notify_cooldowns` JSON

## 配置与数据

| 项 | 位置 | 说明 |
|---|---|---|
| `UPSTREAMHUB_NOTIFICATIONS_QUIETHOURS` | env / config `notifications.quietHours` | `HH:MM-HH:MM`，默认空 = 关闭。跨午夜合法；start==end / 非法时刻报错。viper camelCase 需显式 BindEnv |
| `channels.notify_cooldowns` | 新增文本列 | JSON 对象；键限 `balance_low` / `balance_forecast_low` / `login_failed` / `monitor_failed`；值 = 分钟（int ≥ 0，0 = 不冷却） |

## 边界用例

- **窗口边界**：23:00:00 在窗口内（含）；07:59:59 在内；08:00:00 不在（不含）
- **静默不吞冷却**：23:30 触发被静默（未 claim），07:30 再触发且条件仍成立 → 发送（冷却从 07:30 起算）
- **静默优先于冷却判定**：窗口内不做 cooldown claim（先判静默再判冷却）
- **覆盖值 0**：map 中显式 0 与「键不存在」语义不同（前者关闭冷却、后者用全局）
- **恢复事件**：`login_recovered` / `monitor_recovered` 不参与冷却（现状），受静默时段约束（AC-3）
- **测试发送**：不受静默、不受冷却、不受覆盖影响
- **跨午夜窗口**：`23:00-08:00` 用 OR 语义；`08:00-23:00` 普通窗口用 AND 语义
- **软删除渠道**：不相关（覆盖随 Channel 值传递，无独立查询）

## 非目标

- **不做静默期事件排队 / 出窗汇总**（弱版本；汇总冲刷是 P1-7 日报的基建）
- **不做 per-notification-channel 粒度**（Telegram 及时、邮件日结这类，后续单独做）
- **不做静默时段的 UI 配置**（env only，同其余 Policy；UI 化归 P3-14 配置热更新）
- **不做配置热更新**（改静默窗口 / 覆盖需重启；覆盖随渠道保存即时生效——因为它从 Channel 数据读取，不经 Policy）
- **不做「严重事件豁免静默」**（无事件分级体系，不硬造）

## 关联代码

- `backend/internal/notify/quiet.go` — `QuietHours` / `ParseQuietHours` / `InWindow`（AC-1 / AC-2；测试 `notify/quiet_test.go`）
- `backend/internal/notify/cooldown.go` — `CooldownOverrides` / `ParseCooldownOverrides`（AC-4 解析契约；测试 `notify/cooldown_test.go`）
- `backend/internal/notify/policy.go` — `Policy.QuietHours` 字段
- `backend/internal/notify/notifier.go` — `Message.Cooldown` 字段
- `backend/internal/notify/dispatcher.go` — `Dispatcher.now` / `quiet()` / `Dispatch` / `DispatchRateBatch` 静默短路、`suppress` 覆盖值判定（AC-3 / AC-5；测试 `notify/dispatcher_test.go`）
- `backend/internal/config/config.go` — `NotificationsConfig.QuietHours` + `BindEnv` + Load 时 fail-fast 校验（测试 `config/config_test.go` `TestQuietHoursConfig`）
- `backend/cmd/server/main.go` — `notify.Policy.QuietHours` 接线
- `backend/internal/storage/model.go` — `Channel.NotifyCooldowns`（`channels.notify_cooldowns` 列，AutoMigrate 自动加列）
- `backend/internal/monitor/changes.go` — `cooldownOverrides()` fail-open helper + `balanceLowMessage` 填充（测试 `monitor/changes_test.go`）
- `backend/internal/monitor/forecast.go` — `balanceForecastMessage` 填充
- `backend/internal/monitor/failure.go` — `failureMessage` / `OnSuccess` 填充（测试 `monitor/failure_test.go` `TestFailureMessagesCarryCooldown`）
- `backend/internal/api/channels.go` — `channelInput` / `channelUpdateInput` DTO + `notify.ParseCooldownOverrides` 400 校验（AC-4；测试 `api/channels_cooldown_test.go`，env 门控集成）
- `backend/internal/channel/service.go` — `CreateInput.NotifyCooldowns` / `UpdateInput.NotifyCooldowns` 落库
- `frontend/lib/notify-cooldowns.ts` — `parseNotifyCooldowns` / `buildNotifyCooldowns` / `CooldownFormValues`（AC-6；测试 `lib/notify-cooldowns.test.ts`）
- `frontend/components/monitor/channel-form-dialog.tsx` — 4 个分钟输入框、回填与提交序列化（AC-6）

## 变更记录

- 2026-11-08 — 初稿
- 2026-11-09 — 实现落地（Red → Green 全绿），关联代码回填
