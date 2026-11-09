# 运行时配置热更新（P3-14）

> 状态：已实现（2026-11-09）。

## 背景

cron 调度、通知去抖策略、历史保留策略目前全部来自 env / config.yaml，进程启动时一次性读入：`scheduler.New` 值拷贝 `SchedulerConfig` 后 `AddFunc` 固定 cron 表达式、`notify.Dispatcher` 构造时拷贝 `Policy`、`monitor.Service` 持有 `ForecastConfig` / `balanceRecovery`、`FailureTracker` 持有失败阈值。改任何调度参数或通知策略都要改 env 并重启，而典型部署（Docker `edge` tag / systemd）重启意味着监控断流。roadmap P3-14：改为运行时 UI 可改并落库。

设计决策：

- **热更新范围 = 17 个字段**：调度（balanceCron / rateCron / retentionCron + 3 个保留天数）+ 通知策略（batchRateChanges / minChangePct / balanceLowCooldownMinutes / failureThreshold / failureCooldownMinutes / quietHours / sendMaxAttempts / balanceRecovery）+ 消耗预测（forecast windowDays / alertDays / cooldownMinutes）。数据库、鉴权、日志、端口、metrics token、`scheduler.concurrency` 不纳入（见非目标）。
- **落库载体 = 单行表 `app_settings`**（id 固定 1，17 个类型化列 + updated_at）。不用 key-value 行：字段全部已知且类型明确，typed 列与既有 repo 风格一致，免字符串往返解析。
- **优先级语义**：行不存在 = 沿用 env / config.yaml（现状行为零变化，存量部署无感升级）；一旦通过 UI 保存过（行存在），这 17 个字段**以库为准**，此后 env 改动不再生效（要回到 env 值用「恢复默认」删行）。GET 始终返回"当前生效值"，UI 不感知合并细节。
- **传播方式 = 推送式 Update 方法**：`Dispatcher.UpdatePolicy` / `monitor.Service.UpdateSettings` / `FailureTracker.UpdateThreshold` / `Scheduler.Reconfigure`。所有构造器签名不变（既有调用方与测试零改动），订阅回调的装配集中在 `cmd/server/main.go`（沿用"生产接线保持稳定"约定）。
- **scheduler 换表达式**用 robfig/cron v3 的 `EntryID` + `Remove` + 重新 `AddFunc`：已启动的 cron 动态换调度；正在执行的一轮扫描不打断。

## 行为规格

### AC-1: 持久化与优先级

- **Given** `app_settings` 无行
- **When** 启动 / `GET /api/settings`
- **Then** 17 字段生效值 = env / config.yaml 推导值（与现状逐字段一致）；响应附 `saved: false`
- **Given** `app_settings` 有行
- **Then** 17 字段全部以行为准；`saved: true` + `updated_at`
- **Given** 有行
- **When** `DELETE /api/settings`
- **Then** 删行并立即回退到 env 推导值（各组件同步收到回退后的值），等价于"从未保存过"

### AC-2: 启动装载

- **Given** 库中有行
- **When** 进程启动
- **Then** scheduler 按行内 cron 注册（不是 env cron）；Dispatcher / monitor / FailureTracker 按行内策略初始化；env 值不覆盖库值

### AC-3: 读取

- `GET /api/settings` 返回 17 字段当前生效值 + `saved` + `updated_at`（有行时）

### AC-4: 热生效

- **Given** 任意运行中实例
- **When** `PUT /api/settings` 提交合法配置
- **Then** 落库 + 各组件**免重启**生效：
  - cron 变更：旧 entry 移除、新表达式接管后续触发；进行中的一轮扫描不受影响
  - 通知策略变更：下一次 `Dispatch` 即按新 Policy（冷却时长 / 静默窗口 / 批量合并 / 重试上限 / balanceRecovery 开关）
  - forecast 参数、失败阈值：下一次扫描 / 失败计数即生效
- PUT 返回生效后的完整配置

### AC-5: 校验（fail-fast）

- 非法输入（cron 解析失败、quiet_hours 非法、数值越下限）→ 400 + 具体错误信息；**不落库、不生效、组件保持旧值**
- 空 cron = 不调度该任务（合法，与现状一致）；空 quiet_hours = 关闭静默（合法）

校验规则表：

| 字段 | 规则 |
|---|---|
| balance_cron / rate_cron / retention_cron | 空 = 不调度；非空必须能被 6 段（含秒）cron parser 解析 |
| retention_monitor_logs_days / balance_snapshots_days / notification_logs_days | >= 0；0 = 该表不清理 |
| batch_rate_changes / balance_recovery | bool，任意 |
| min_change_pct | >= 0 |
| balance_low_cooldown_minutes / failure_cooldown_minutes / forecast_cooldown_minutes | >= 0；0 = 不冷却 |
| failure_threshold | >= 1（1 = 每次失败即告警） |
| quiet_hours | 空 = 关；非空合法 `HH:MM-HH:MM`（复用 `notify.ParseQuietHours`） |
| send_max_attempts | >= 1 |
| forecast_window_days | >= 1 |
| forecast_alert_days | >= 0；0 = 关闭预测告警 |

### AC-6: 前端设置卡片

- 设置页新增「运行时配置」卡片：按 调度 / 通知 / 预测 三组展示 17 字段，GET 当前生效值预填
- 保存 → `PUT` + 成功 toast + 刷新显示；恢复默认 → `DELETE` + 刷新
- 原「调度计划」卡片的硬编码文案（"每 15 分钟 / 每 30 分钟"）改为读实际生效 cron
- 字段带行内校验（提交前提示），非法值不发出请求

### AC-7: 范围边界（钉住现状）

- server.* / database.* / auth.* / security / log.* / updater.* / metricsToken / scheduler.concurrency 仍为 env-only，重启生效；API 不接受这些字段（出现在请求体中被忽略）

## 边界用例

- **保存前（无行）**：全部行为与当前版本一致（回归保障，既有测试零改动必须全绿）
- **保存后改 env 再重启**：库优先，env 改动被忽略；文档写明，要回 env 值走「恢复默认」
- **PUT 与扫描并发**：组件 Update 方法读写锁并发安全；扫描进行中改 cron 不打断本轮
- **静默时段开关切换**：下一次 Dispatch 生效，不追溯已发出的通知
- **失败阈值调小**：既有 streak 计数不变，下一次失败按新阈值判定（P1-4 streak 语义不受影响）
- **重置失败**：删行 DB 报错 → 500，组件保持库值（不半途回退）
- **三个保留天数全 0**：retention entry 不注册（与现状 `hasRetention` 语义一致）
- **并发 PUT**：后写覆盖先写（单用户后台，不做乐观锁）；每次 PUT 全量 17 字段，无部分更新

## 非目标

- **不做 per-field 覆盖**（行存在即整行生效，避免 NULL 语义蔓延；UI 始终提交全量）
- **不做配置变更审计历史**（只留 updated_at；审计需求将来单开 spec）
- **不做 scheduler.concurrency 热更新**（该字段至今未被使用，属 P3-12 并发扫描范畴）
- **不做 auth / 数据库 / 端口等引导配置的热更新**（安全敏感；连接重建复杂度不值）
- **不做多用户权限区分**（单用户后台，登录即管理员）

## 关联代码

- `backend/internal/settings/settings.go` — `FromConfig` / `Validate` / `SchedulerConfigOf` / `PolicyOf` / `MonitorForecastOf`（`storage.AppSettings` ↔ 各组件配置转换，纯函数）；`ValidationError{Field, Reason}` → API 400
- `backend/internal/settings/service.go` — `Store` 窄接口 + `Service`（`NewService` / `Init` / `Current` / `Saved` / `OnApply` / `Update` / `Reset`；applier 在锁内按序执行）
- `backend/internal/storage/model.go` — `AppSettings` 17 列（`TableName` = `app_settings`）；`storage/storage.go` AutoMigrate
- `backend/internal/storage/appsettings.go` — `NewAppSettings` / `Get`（无行 → nil）/ `Save`（固定 id=1）/ `Delete`
- `backend/internal/storage/appsettings_test.go` — env 门控 PostgreSQL 集成（回退 / 覆盖 / 删行重存）
- `backend/internal/notify/dispatcher.go` — `UpdatePolicy` + `policySnapshot()`（RWMutex，Dispatch 并发安全）
- `backend/internal/monitor/service.go` — `UpdateSettings` + `settingsSnapshot()`；`monitor/failure.go` — `UpdateThreshold` + `thresholdSnapshot()`
- `backend/internal/scheduler/scheduler.go` — 导出 `ParseCron`（与 `cron.WithSeconds()` 同 parser）+ `Reconfigure`（先校验后重注册，无半改状态）+ `registerEntries` 空表达式过滤
- `backend/internal/api/settings.go` — `GET` / `PUT` / `DELETE /api/settings`（`settingsView` 平铺 + `saved`；`errors.As` 映射 `ValidationError` → 400）
- `backend/cmd/server/main.go` — 装配：`Init` 启动装载 → 按 `Current()` 构造组件 → `sch.Start` → 4 个 `OnApply` 回调
- `frontend/lib/api-types.ts`（`RuntimeSettings`）/ `lib/queries.ts`（`useRuntimeSettings`）/ `components/settings/runtime-settings-card.tsx` / `app/settings-page.tsx`（调度计划卡片改读实际生效 cron）

## 变更记录

- 2026-11-09 — 初稿
- 2026-11-09 — 实现完成（Red → Green：`settings` 包 / storage / notify / monitor / scheduler / api / main 装配 / 前端卡片）
