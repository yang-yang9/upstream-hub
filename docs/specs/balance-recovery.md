# 余额恢复通知（P0-3）

> 状态：已实现（2026-11-09）。测试见各关联文件旁注。

## 背景

余额低于阈值的告警（`balance_low`）只有触发、没有恢复：用户收到"余额低于阈值"后充值了，无法从通知里确认"已经好了"（roadmap P0-3）。P1-4 已为失败类事件（`login_recovered` / `monitor_recovered`）建立了"状态持久化 + 恢复事件"模式，本 spec 把同一模式补到余额维度。

设计决策：

- **默认关闭**：`UPSTREAMHUB_NOTIFICATIONS_BALANCERECOVERY`（bool，默认 false）控制恢复通知的**派发**。用户要求默认 7×24 关闭、有需要再打开（2026-11-09 确认）。
- **状态维护始终开启**：`channels.balance_low_active` 列由 monitor 在每次余额扫描时维护（标记 / 清除），不受开关影响。这样打开开关后，存量"正在低余额"的渠道立即能收到恢复通知，不需要重新触发一轮低告警。开关关闭时对通知行为零变化（只是库表多一列维护）。
- **恢复事件不参与冷却**（沿用 P1-4 惯例：`cooldownForEvent` 对非冷却事件返回 not-eligible），受静默时段约束（P1-6），照常携带 per-channel 冷却覆盖（Dispatcher 对非冷却事件忽略它）。
- **阈值附近抖动是已知边界**：余额在阈值上下反复横跳时，每次上穿都会发一条恢复（低告警侧有冷却节流，恢复侧没有）。不在本期加滞回 / 冷却，见"非目标"。

## 行为规格

### AC-1: 默认关闭

- **Given** 未配置 `UPSTREAMHUB_NOTIFICATIONS_BALANCERECOVERY`（默认 false）
- **When** 渠道余额低于阈值后回升
- **Then** 不派发任何恢复通知（通知行为与现状完全一致）；`balance_low_active` 状态照常维护

### AC-2: 低余额状态标记

- **Given** 渠道 `BalanceThreshold > 0`，`BalanceLowActive = false`
- **When** 余额扫描得到 `balance < threshold`
- **Then** 落库 `balance_low_active = true`（与本次是否实际派发 balance_low 告警无关——冷却 / 静默压掉告警不撤销标记，与 P1-4 streak 语义一致）
- **Given** `BalanceLowActive = true`
- **When** 余额扫描得到 `balance >= threshold`
- **Then** 落库 `balance_low_active = false`；若开关开启，派发恢复通知（AC-3）
- **Given** `BalanceThreshold <= 0`（阈值未配置 / 已删除）且 `BalanceLowActive = true`
- **When** 任意余额扫描
- **Then** 静默清除标记（不发通知——阈值已删，"恢复"无从界定）

### AC-3: 恢复通知派发

- **Given** 开关开启、`BalanceLowActive = true`、`BalanceThreshold > 0`
- **When** 余额扫描得到 `balance >= threshold`
- **Then** 派发一条 `balance_recovered` 事件消息：
  - Subject：`[upstream-hub] <渠道名> 余额已恢复`
  - Body：`当前余额: <balance 4 位小数>，阈值: <threshold 4 位小数>`
  - 携带 `Cooldown: cooldownOverrides(c)`（与其他事件一致）
- 状态持久化失败：warn 日志后照发通知（fail-open，漏标记可接受、漏通知不可接受，与 P1-4 同语义）
- 手动刷新（API 触发的 `RefreshBalance`）走同一代码路径，同样触发状态迁移与恢复检测

### AC-4: 恢复事件的既有机制（钉住现状）

- `balance_recovered` 不参与冷却（`cooldownForEvent` not-eligible，每次上穿都发）
- 受静默时段约束（P1-6：窗口内不推送、不占冷却名额）
- 测试发送（`Send`）不受影响

### AC-5: 事件枚举与展示

- `storage.EventBalanceRecovered = "balance_recovered"` 纳入事件常量；通知日志按事件名落库（现状机制，无新表）
- 前端 `NotificationEvent` 类型补 `"balance_recovered"`；告警流（bottom-panels eventMeta）补图标映射（success 色系，与 login_recovered 同档）

### AC-6: 状态列持久化

- `Channel.BalanceLowActive bool`（gorm `default:false`，json `balance_low_active`），AutoMigrate 自动加列；新渠道默认 false
- storage 提供 `SetBalanceLowActive(channelID, active)`，只更新该列；渠道 GET / list 原样返回该字段
- 该字段为 monitor 维护的运行时状态，**渠道创建 / 更新 API 不接受**（提交了也忽略）

## 配置与数据

| 项 | 位置 | 说明 |
|---|---|---|
| `UPSTREAMHUB_NOTIFICATIONS_BALANCERECOVERY` | env / config `notifications.balanceRecovery` | bool，默认 false = 关。viper camelCase 需显式 BindEnv |
| `channels.balance_low_active` | 新增 bool 列 | monitor 每次余额扫描维护；开关关闭时也维护 |

## 边界用例

- **首次低于阈值**：标记 + 派发 balance_low（既有逻辑不变，本次迁移不加通知）
- **持续低于**：状态不变（已是 true，不重复写库）
- **回升**：清标记 + （开关开时）一条 balance_recovered
- **冷却期内的低告警**：balance_low 被冷却压掉，但状态照常标记——冷却压制是通知层节流，不是状态层语义
- **静默时段内回升**：恢复通知被静默压掉，状态照常清除（静默不排队、不补发，P1-6 语义）
- **扫描失败**：本次不迁移状态（无新余额数据）；状态保持
- **阈值抖动**：low → high → low → high 产生两条恢复通知（已知边界，见非目标）
- **禁用监控的渠道**：不扫描，状态冻结；重新启用后首次扫描按新余额迁移（若已回升则补一条恢复——用户视角合理：上次看到的是低告警）
- **渠道删除**：状态随行删除
- **余额相等于阈值**：`balance >= threshold` 视为已恢复（与 balanceLowMessage 的严格小于语义对称：`balance < threshold` 才算低）

## 非目标

- **不做恢复通知的冷却 / 滞回**（阈值抖动重复通知靠用户调阈值规避；量化后若成痛点再做）
- **不做 balance_forecast_low 的恢复**（预测是"按当前速率推算"，恢复语义复杂，且充值后预测自然变化）
- **不做"低告警实际送达才算标记"**（需要 Dispatch 返回送达结果，改 Dispatcher 契约；P1-4 同样不做）
- **不做开关的 UI 配置**（env only，同其余 Policy；归 P3-14 热更新）

## 关联代码

- `backend/internal/storage/model.go` — `EventBalanceRecovered` 事件常量 + `Channel.BalanceLowActive` 列（AC-5 / AC-6；测试 `storage/channels_balance_test.go`，env 门控集成）
- `backend/internal/storage/channels.go` — `SetBalanceLowActive` 单列更新（AC-6）
- `backend/internal/monitor/changes.go` — `balancePlan` / `balanceStatePlan` 状态迁移纯函数（AC-2 / AC-3；测试 `monitor/changes_test.go` `TestBalanceStatePlan` / `TestBalanceRecoveryMessage`）
- `backend/internal/monitor/service.go` — `Service.balanceRecovery` 开关 + `trackBalanceState` 接线（状态持久化 fail-open、派发恢复）；`RefreshBalance` 尾部调用（AC-3）
- `backend/internal/config/config.go` — `NotificationsConfig.BalanceRecovery` + `BindEnv`（AC-1；测试 `config/config_test.go` `TestBalanceRecoveryConfig`）
- `backend/cmd/server/main.go` — `cfg.Notifications.BalanceRecovery` 注入 `monitor.NewService`
- `backend/internal/notify/dispatcher.go` — 无改动：`balance_recovered` 不在 `cooldownForEvent` 映射中，天然 not-eligible（AC-4 钉住现状）
- `frontend/lib/api-types.ts` — `NotificationEvent` 补 `"balance_recovered"`（AC-5）
- `frontend/components/monitor/bottom-panels.tsx` — `eventMeta` 补 `balance_recovered`（Wallet / success，AC-5）

## 变更记录

- 2026-11-09 — 初稿
- 2026-11-09 — 实现落地（Red → Green 全绿），关联代码回填
