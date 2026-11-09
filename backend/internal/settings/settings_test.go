package settings

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/worryzyy/upstream-hub/internal/config"
	"github.com/worryzyy/upstream-hub/internal/monitor"
	"github.com/worryzyy/upstream-hub/internal/notify"
	"github.com/worryzyy/upstream-hub/internal/storage"
)

// TDD red：settings 包尚不存在（将在 internal/settings 实现）。
// 对应 spec：docs/specs/runtime-settings.md。

func testConfig() *config.Config {
	return &config.Config{
		Scheduler: config.SchedulerConfig{
			BalanceCron: "37 */15 * * * *",
			RateCron:    "13 */30 * * * *",
			Retention: config.RetentionConfig{
				Cron:                 "0 17 3 * * *",
				MonitorLogsDays:      30,
				BalanceSnapshotsDays: 90,
				NotificationLogsDays: 90,
			},
		},
		Notifications: config.NotificationsConfig{
			BatchRateChanges:          true,
			MinChangePct:              0.5,
			BalanceLowCooldownMinutes: 60,
			FailureThreshold:          3,
			FailureCooldownMinutes:    45,
			QuietHours:                "23:00-07:00",
			SendMaxAttempts:           3,
			BalanceRecovery:           true,
		},
		Forecast: config.ForecastConfig{WindowDays: 7, AlertDays: 3, CooldownMinutes: 720},
	}
}

func TestFromConfig(t *testing.T) {
	got := FromConfig(testConfig())
	want := storage.AppSettings{
		BalanceCron:                   "37 */15 * * * *",
		RateCron:                      "13 */30 * * * *",
		RetentionCron:                 "0 17 3 * * *",
		RetentionMonitorLogsDays:      30,
		RetentionBalanceSnapshotsDays: 90,
		RetentionNotificationLogsDays: 90,
		BatchRateChanges:              true,
		MinChangePct:                  0.5,
		BalanceLowCooldownMinutes:     60,
		FailureThreshold:              3,
		FailureCooldownMinutes:        45,
		QuietHours:                    "23:00-07:00",
		SendMaxAttempts:               3,
		BalanceRecovery:               true,
		ForecastWindowDays:            7,
		ForecastAlertDays:             3,
		ForecastCooldownMinutes:       720,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FromConfig 17 字段映射不完整或不对：\n got  %+v\n want %+v", got, want)
	}
}

func TestValidate(t *testing.T) {
	base := FromConfig(testConfig())
	for _, tc := range []struct {
		name      string
		mutate    func(*storage.AppSettings)
		wantErr   bool
		wantField string
	}{
		{"合法全量", func(*storage.AppSettings) {}, false, ""},
		{"三个 cron 全空 = 不调度（合法）", func(s *storage.AppSettings) {
			s.BalanceCron, s.RateCron, s.RetentionCron = "", "", ""
		}, false, ""},
		{"quiet_hours 空 = 关静默（合法）", func(s *storage.AppSettings) {
			s.QuietHours = ""
		}, false, ""},
		{"balance_cron 5 段（缺秒）", func(s *storage.AppSettings) {
			s.BalanceCron = "*/15 * * * *"
		}, true, "balance_cron"},
		{"rate_cron 垃圾串", func(s *storage.AppSettings) {
			s.RateCron = "not-a-cron"
		}, true, "rate_cron"},
		{"retention_cron 6 段也合法（含秒）", func(s *storage.AppSettings) {
			s.RetentionCron = "0 17 3 * * *"
		}, false, ""},
		{"quiet_hours 非法时刻", func(s *storage.AppSettings) {
			s.QuietHours = "25:00-07:00"
		}, true, "quiet_hours"},
		{"quiet_hours start==end 歧义", func(s *storage.AppSettings) {
			s.QuietHours = "07:00-07:00"
		}, true, "quiet_hours"},
		{"monitor_logs_days 负", func(s *storage.AppSettings) {
			s.RetentionMonitorLogsDays = -1
		}, true, "retention_monitor_logs_days"},
		{"balance_snapshots_days 负", func(s *storage.AppSettings) {
			s.RetentionBalanceSnapshotsDays = -1
		}, true, "retention_balance_snapshots_days"},
		{"notification_logs_days 负", func(s *storage.AppSettings) {
			s.RetentionNotificationLogsDays = -1
		}, true, "retention_notification_logs_days"},
		{"三个保留天数全 0 = 不清理（合法）", func(s *storage.AppSettings) {
			s.RetentionMonitorLogsDays, s.RetentionBalanceSnapshotsDays, s.RetentionNotificationLogsDays = 0, 0, 0
		}, false, ""},
		{"min_change_pct 负", func(s *storage.AppSettings) {
			s.MinChangePct = -0.1
		}, true, "min_change_pct"},
		{"balance_low_cooldown_minutes 负", func(s *storage.AppSettings) {
			s.BalanceLowCooldownMinutes = -1
		}, true, "balance_low_cooldown_minutes"},
		{"failure_cooldown_minutes 负", func(s *storage.AppSettings) {
			s.FailureCooldownMinutes = -1
		}, true, "failure_cooldown_minutes"},
		{"三个冷却分钟全 0 = 不冷却（合法）", func(s *storage.AppSettings) {
			s.BalanceLowCooldownMinutes, s.FailureCooldownMinutes, s.ForecastCooldownMinutes = 0, 0, 0
		}, false, ""},
		{"failure_threshold 0", func(s *storage.AppSettings) {
			s.FailureThreshold = 0
		}, true, "failure_threshold"},
		{"failure_threshold 1 = 每次失败即告警（合法）", func(s *storage.AppSettings) {
			s.FailureThreshold = 1
		}, false, ""},
		{"send_max_attempts 0", func(s *storage.AppSettings) {
			s.SendMaxAttempts = 0
		}, true, "send_max_attempts"},
		{"forecast_window_days 0", func(s *storage.AppSettings) {
			s.ForecastWindowDays = 0
		}, true, "forecast_window_days"},
		{"forecast_alert_days 负", func(s *storage.AppSettings) {
			s.ForecastAlertDays = -1
		}, true, "forecast_alert_days"},
		{"forecast_alert_days 0 = 关闭预测告警（合法）", func(s *storage.AppSettings) {
			s.ForecastAlertDays = 0
		}, false, ""},
	} {
		s := base
		tc.mutate(&s)
		err := Validate(s)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: Validate 应报错", tc.name)
				continue
			}
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Errorf("%s: 错误应为 *ValidationError，got %T (%v)", tc.name, err, err)
				continue
			}
			if ve.Field != tc.wantField {
				t.Errorf("%s: 错误字段 = %q, want %q", tc.name, ve.Field, tc.wantField)
			}
		} else if err != nil {
			t.Errorf("%s: Validate 不应报错，got %v", tc.name, err)
		}
	}
}

// 各组件类型转换：字段一一对应，分钟 → time.Duration，quiet_hours → 结构化窗口。
func TestConversions(t *testing.T) {
	s := FromConfig(testConfig())

	sc := SchedulerConfigOf(s)
	wantSc := config.SchedulerConfig{
		BalanceCron: "37 */15 * * * *",
		RateCron:    "13 */30 * * * *",
		Retention: config.RetentionConfig{
			Cron:                 "0 17 3 * * *",
			MonitorLogsDays:      30,
			BalanceSnapshotsDays: 90,
			NotificationLogsDays: 90,
		},
	}
	if !reflect.DeepEqual(sc, wantSc) {
		t.Errorf("SchedulerConfigOf:\n got  %+v\n want %+v", sc, wantSc)
	}

	quiet, err := notify.ParseQuietHours("23:00-07:00")
	if err != nil {
		t.Fatalf("parse quiet hours: %v", err)
	}
	p := PolicyOf(s)
	wantP := notify.Policy{
		BatchRateChanges:   true,
		MinChangePct:       0.5,
		BalanceLowCooldown: 60 * time.Minute,
		ForecastCooldown:   720 * time.Minute,
		FailureCooldown:    45 * time.Minute,
		QuietHours:         quiet,
		SendMaxAttempts:    3,
	}
	if !reflect.DeepEqual(p, wantP) {
		t.Errorf("PolicyOf:\n got  %+v\n want %+v", p, wantP)
	}

	f := MonitorForecastOf(s)
	wantF := monitor.ForecastConfig{WindowDays: 7, AlertDays: 3}
	if !reflect.DeepEqual(f, wantF) {
		t.Errorf("MonitorForecastOf:\n got  %+v\n want %+v", f, wantF)
	}
}
