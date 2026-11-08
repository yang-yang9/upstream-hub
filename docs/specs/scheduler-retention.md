# Spec：调度与数据保留

> robfig/cron 触发周期扫描；按配置清理过期的监控日志 / 余额快照 / 通知日志。

## 背景

`scheduler.Scheduler` 用 cron 表达式（含秒）触发余额扫描（balanceCron）、倍率扫描（rateCron）与数据保留（retention.cron）。保留任务是纯逻辑 `planRetention`：把 `RetentionConfig` 的三个天数转成有序的任务列表（表名 + cutoff），执行层分派到对应 storage 删除方法。三张表相互独立：一张失败不影响其它，失败只写日志不中断。

## 行为规格

### AC-1: cron 接线

- **Given** balanceCron / rateCron / retention.cron 配置非空
- **When** `Start`
- **Then** 各自注册到 cron（WithSeconds），retention 额外要求至少一个保留天数 > 0；空表达式跳过注册不报错；`Stop` 等待运行中的任务退出

### AC-2: 保留计划（planRetention）

- **Given** RetentionConfig 某项 days > 0
- **When** `planRetention(cfg, now)`
- **Then** 产出对应任务（cutoff = now - days×24h），固定顺序 monitor_logs → balance_snapshots → notification_logs；days ≤ 0 的项不出现在计划里

### AC-3: 删除语义

- **Given** 表中存在早于 / 等于 / 晚于 cutoff 的行
- **When** 执行保留任务
- **Then** `DeleteBefore` 严格小于 cutoff：早于的删除、**等于的保留**；返回删除行数

### AC-4: 失败隔离

- **Given** 某张表的删除报错（如锁冲突）
- **When** runRetention
- **Then** 其余表照常清理；错误写 `retention <table> failed` 日志；成功且 n>0 写 `retention <table> deleted`（rows / before 字段）

## 边界用例

| 边界 | 期望 | 测试 |
|------|------|------|
| 全部 days ≤ 0 | hasRetention=false，不注册 cron；plan 为空 | `TestHasRetention` / `TestPlanRetention` |
| 部分 days = 0 | 只保留 >0 的表 | `TestPlanRetention` |
| days=1 的 cutoff | now-24h | `TestPlanRetention` |
| 删除恰好等于 cutoff | 保留（严格小于） | `TestNotificationsLogs` / `TestRatesBalanceHistoryAndRetention` / `TestMonitorLogsDeleteBefore` |

## 非目标

- 不做表分区 / 归档（数据量大到删除慢时再立 spec）。
- 不做保留任务的手动触发 API（调 cron 即可）。
- 不做扫描任务本身的并发控制（每类 cron 串行触发，超时 5 分钟兜底）。

## 关联代码

- 实现：`backend/internal/scheduler/scheduler.go` — `Start` / `Stop` / `runRetention` / `runRetentionTask` / `hasRetention`
- 实现：`backend/internal/scheduler/retention.go` — `planRetention` / `retentionTask` / `retentionKind.table()`
- 单元测试：`backend/internal/scheduler/scheduler_test.go` / `retention_test.go`
- 集成测试（env 门控，删除语义）：`backend/internal/storage/*_test.go` 的 Delete* 用例

## 变更记录

- 2026-11-08 — 初稿（SDD 改造回填，AC 对应 Phase 3–6 测试）
