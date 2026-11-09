package scheduler

import (
	"log/slog"
	"reflect"
	"testing"

	"github.com/worryzyy/upstream-hub/internal/config"
)

// ---------- 热更新（P3-14，docs/specs/runtime-settings.md AC-4） ----------

// TDD red：ParseCron / Reconfigure 尚不存在（将在 scheduler.go 实现）。
// ParseCron 与 cron.New(cron.WithSeconds()) 用同一 parser：
// 6 段（含秒）表达式合法，5 段 / 垃圾串报错。
func TestParseCron(t *testing.T) {
	for _, tc := range []struct {
		name    string
		expr    string
		wantErr bool
	}{
		{"6 段含秒", "37 */15 * * * *", false},
		{"固定时刻", "0 17 3 * * *", false},
		{"descriptor", "@every 1h", false},
		{"5 段（缺秒）", "*/15 * * * *", true},
		{"7 段（多一段）", "0 37 */15 * * * *", true},
		{"垃圾串", "not-a-cron", true},
		{"空串（调用方单独判空）", "", true},
	} {
		_, err := ParseCron(tc.expr)
		if tc.wantErr && err == nil {
			t.Errorf("%s: ParseCron(%q) 应报错", tc.name, tc.expr)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("%s: ParseCron(%q) 不应报错，got %v", tc.name, tc.expr, err)
		}
	}
}

// Reconfigure 在运行中的 cron 上换表达式：旧 entry 移除、新表达式接管。
// 测试用 2 月 30/31 日这类日历上不存在的日期，保证 cron 不会真正触发任务，
// monitor 传 nil 也安全。
func TestReconfigure(t *testing.T) {
	s := New(config.SchedulerConfig{
		BalanceCron: "0 0 0 31 2 *",
		RateCron:    "0 0 0 31 2 *",
	}, nil, nil, nil, nil, slog.Default())
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()
	if got := len(s.cron.Entries()); got != 2 {
		t.Fatalf("初始 entries = %d, want 2", got)
	}

	// 换表达式 + 打开 retention：3 个 entry，schedule 全部换成新表达式。
	err := s.Reconfigure(config.SchedulerConfig{
		BalanceCron: "0 0 0 30 2 *",
		RateCron:    "0 5 0 31 2 *",
		Retention:   config.RetentionConfig{Cron: "0 10 0 31 2 *", MonitorLogsDays: 7},
	})
	if err != nil {
		t.Fatalf("Reconfigure: %v", err)
	}
	entries := s.cron.Entries()
	if got := len(entries); got != 3 {
		t.Fatalf("Reconfigure 后 entries = %d, want 3", got)
	}
	for _, expr := range []string{"0 0 0 30 2 *", "0 5 0 31 2 *", "0 10 0 31 2 *"} {
		want, err := ParseCron(expr)
		if err != nil {
			t.Fatalf("ParseCron(%q): %v", expr, err)
		}
		found := false
		for _, e := range entries {
			if reflect.DeepEqual(e.Schedule, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("entries 中找不到新表达式 %q 的 schedule", expr)
		}
	}

	// 非法表达式：报错且现场保持不变（不出现半改状态）。
	if err := s.Reconfigure(config.SchedulerConfig{BalanceCron: "bad"}); err == nil {
		t.Error("非法表达式应报错")
	}
	if got := len(s.cron.Entries()); got != 3 {
		t.Errorf("非法 Reconfigure 后 entries = %d, want 3（现场不变）", got)
	}

	// 全空 + retention 关闭：entry 全部撤销。
	if err := s.Reconfigure(config.SchedulerConfig{}); err != nil {
		t.Fatalf("Reconfigure 空: %v", err)
	}
	if got := len(s.cron.Entries()); got != 0 {
		t.Errorf("全空 Reconfigure 后 entries = %d, want 0", got)
	}
}

// 未 Start 时 Reconfigure 也可用（等价于先改配置再 Start）。
func TestReconfigureBeforeStart(t *testing.T) {
	s := New(config.SchedulerConfig{}, nil, nil, nil, nil, slog.Default())
	if got := len(s.cron.Entries()); got != 0 {
		t.Fatalf("初始 entries = %d, want 0", got)
	}
	if err := s.Reconfigure(config.SchedulerConfig{BalanceCron: "0 0 0 31 2 *"}); err != nil {
		t.Fatalf("Reconfigure: %v", err)
	}
	if got := len(s.cron.Entries()); got != 1 {
		t.Errorf("Reconfigure 后 entries = %d, want 1", got)
	}
	// Start 不应重复注册。
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()
	if got := len(s.cron.Entries()); got != 1 {
		t.Errorf("Start 后 entries = %d, want 1（不重复注册）", got)
	}
}

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
