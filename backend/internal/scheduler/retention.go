// retention.go 抽出保留清理的纯函数，使任务规划逻辑可以脱离 repo 单测。
// 语义与原先 scheduler.go 内联实现一致，对应 spec：docs/specs/scheduler-retention.md。
package scheduler

import (
	"time"

	"github.com/worryzyy/upstream-hub/internal/config"
)

// retentionKind 标识一个保留清理任务对应的表。
type retentionKind int

const (
	retentionMonitorLogs retentionKind = iota
	retentionBalanceSnapshots
	retentionNotificationLogs
)

// table 返回日志用的表名标签。
func (k retentionKind) table() string {
	switch k {
	case retentionMonitorLogs:
		return "monitor_logs"
	case retentionBalanceSnapshots:
		return "balance_snapshots"
	default:
		return "notification_logs"
	}
}

// retentionTask 一张表的清理计划：删除 cutoff 之前的行。
type retentionTask struct {
	kind   retentionKind
	cutoff time.Time
}

// planRetention 依据保留配置生成清理任务。
// 顺序固定 monitor_logs → balance_snapshots → notification_logs；天数为 0 的表跳过。
func planRetention(r config.RetentionConfig, now time.Time) []retentionTask {
	var tasks []retentionTask
	if r.MonitorLogsDays > 0 {
		tasks = append(tasks, retentionTask{kind: retentionMonitorLogs, cutoff: now.AddDate(0, 0, -r.MonitorLogsDays)})
	}
	if r.BalanceSnapshotsDays > 0 {
		tasks = append(tasks, retentionTask{kind: retentionBalanceSnapshots, cutoff: now.AddDate(0, 0, -r.BalanceSnapshotsDays)})
	}
	if r.NotificationLogsDays > 0 {
		tasks = append(tasks, retentionTask{kind: retentionNotificationLogs, cutoff: now.AddDate(0, 0, -r.NotificationLogsDays)})
	}
	return tasks
}
