package scheduler

import (
	"testing"

	"github.com/worryzyy/upstream-hub/internal/config"
)

// 特征测试：钉住 hasRetention 的当前行为（任一保留天数 > 0 即需要清理任务）。
// 对应 spec：docs/specs/scheduler-retention.md。

func TestHasRetention(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  config.RetentionConfig
		want bool
	}{
		{"all zero disables retention", config.RetentionConfig{}, false},
		{"monitor logs days only", config.RetentionConfig{MonitorLogsDays: 7}, true},
		{"balance snapshots days only", config.RetentionConfig{BalanceSnapshotsDays: 30}, true},
		{"notification logs days only", config.RetentionConfig{NotificationLogsDays: 14}, true},
		{"cron set but no days", config.RetentionConfig{Cron: "0 3 * * *"}, false},
		{"cron and days", config.RetentionConfig{Cron: "0 3 * * *", MonitorLogsDays: 7}, true},
	} {
		s := &Scheduler{cfg: config.SchedulerConfig{Retention: tc.cfg}}
		if got := s.hasRetention(); got != tc.want {
			t.Errorf("%s: hasRetention = %v, want %v", tc.name, got, tc.want)
		}
	}
}
