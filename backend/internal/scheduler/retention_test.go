package scheduler

import (
	"testing"
	"time"

	"github.com/worryzyy/upstream-hub/internal/config"
)

// TDD red：planRetention 尚不存在（将在 scheduler/retention.go 抽出）。
// 测试钉住 runRetention 的顺序（monitor_logs → balance_snapshots → notification_logs）
// 与 cutoff = now.AddDate(0, 0, -days) 算法。
// 对应 spec：docs/specs/scheduler-retention.md。

func TestPlanRetention(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)

	// 全部天数未配置：无任务。
	if tasks := planRetention(config.RetentionConfig{}, now); len(tasks) != 0 {
		t.Errorf("empty config should plan nothing, got %v", tasks)
	}

	// 只开一个。
	tasks := planRetention(config.RetentionConfig{MonitorLogsDays: 7}, now)
	if len(tasks) != 1 || tasks[0].kind != retentionMonitorLogs {
		t.Fatalf("monitor logs only: tasks = %+v", tasks)
	}
	if want := now.AddDate(0, 0, -7); !tasks[0].cutoff.Equal(want) {
		t.Errorf("cutoff = %v, want %v", tasks[0].cutoff, want)
	}

	// 三表全开：顺序固定，cutoff 各自独立。
	tasks = planRetention(config.RetentionConfig{
		MonitorLogsDays:       1,
		BalanceSnapshotsDays:  2,
		NotificationLogsDays:  3,
	}, now)
	if len(tasks) != 3 {
		t.Fatalf("all enabled: len(tasks) = %d, want 3", len(tasks))
	}
	wantKinds := []retentionKind{retentionMonitorLogs, retentionBalanceSnapshots, retentionNotificationLogs}
	wantCutoffs := []time.Duration{-24 * time.Hour, -48 * time.Hour, -72 * time.Hour}
	for i := range tasks {
		if tasks[i].kind != wantKinds[i] {
			t.Errorf("tasks[%d].kind = %v, want %v", i, tasks[i].kind, wantKinds[i])
		}
		if want := now.Add(wantCutoffs[i]); !tasks[i].cutoff.Equal(want) {
			t.Errorf("tasks[%d].cutoff = %v, want %v", i, tasks[i].cutoff, want)
		}
	}

	// 中间一个为 0：跳过该任务，顺序保持。
	tasks = planRetention(config.RetentionConfig{
		MonitorLogsDays:      1,
		NotificationLogsDays: 3,
	}, now)
	if len(tasks) != 2 || tasks[0].kind != retentionMonitorLogs || tasks[1].kind != retentionNotificationLogs {
		t.Errorf("gap config: tasks = %+v", tasks)
	}
}
