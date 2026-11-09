// Package settings 运行时配置热更新（P3-14，docs/specs/runtime-settings.md）：
// env / config.yaml 推导的 17 字段基线 + app_settings 单行表覆盖 + 校验 +
// 各组件配置类型转换。传播由 Service 的 OnApply 订阅推送（见 service.go）。
package settings

import (
	"fmt"
	"time"

	"github.com/worryzyy/upstream-hub/internal/config"
	"github.com/worryzyy/upstream-hub/internal/monitor"
	"github.com/worryzyy/upstream-hub/internal/notify"
	"github.com/worryzyy/upstream-hub/internal/scheduler"
	"github.com/worryzyy/upstream-hub/internal/storage"
)

// ValidationError 字段级校验错误。API 层据此映射 400（其余错误一律 500）。
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("invalid %s: %s", e.Field, e.Reason)
}

// FromConfig 从 env / config.yaml 推导 17 字段基线。无行时进程行为与
// 该基线逐字段一致（存量部署零变化）。
func FromConfig(cfg *config.Config) storage.AppSettings {
	return storage.AppSettings{
		BalanceCron:                   cfg.Scheduler.BalanceCron,
		RateCron:                      cfg.Scheduler.RateCron,
		RetentionCron:                 cfg.Scheduler.Retention.Cron,
		RetentionMonitorLogsDays:      cfg.Scheduler.Retention.MonitorLogsDays,
		RetentionBalanceSnapshotsDays: cfg.Scheduler.Retention.BalanceSnapshotsDays,
		RetentionNotificationLogsDays: cfg.Scheduler.Retention.NotificationLogsDays,
		BatchRateChanges:              cfg.Notifications.BatchRateChanges,
		MinChangePct:                  cfg.Notifications.MinChangePct,
		BalanceLowCooldownMinutes:     cfg.Notifications.BalanceLowCooldownMinutes,
		FailureThreshold:              cfg.Notifications.FailureThreshold,
		FailureCooldownMinutes:        cfg.Notifications.FailureCooldownMinutes,
		QuietHours:                    cfg.Notifications.QuietHours,
		SendMaxAttempts:               cfg.Notifications.SendMaxAttempts,
		BalanceRecovery:               cfg.Notifications.BalanceRecovery,
		ForecastWindowDays:            cfg.Forecast.WindowDays,
		ForecastAlertDays:             cfg.Forecast.AlertDays,
		ForecastCooldownMinutes:       cfg.Forecast.CooldownMinutes,
	}
}

// Validate fail-fast 校验（docs/specs/runtime-settings.md AC-5 规则表）。
// 返回 *ValidationError 时调用方拒绝落库、拒绝生效。
// 注意：只覆盖 17 个热更新字段的下限与格式；范围外字段由 API 载荷形状天然忽略。
func Validate(s storage.AppSettings) error {
	for _, tc := range []struct {
		field string
		expr  string
	}{
		{"balance_cron", s.BalanceCron},
		{"rate_cron", s.RateCron},
		{"retention_cron", s.RetentionCron},
	} {
		if tc.expr == "" {
			continue // 空 = 不调度该任务（合法）
		}
		if _, err := scheduler.ParseCron(tc.expr); err != nil {
			return &ValidationError{Field: tc.field, Reason: "需要 6 段（含秒）cron 表达式"}
		}
	}
	if s.QuietHours != "" {
		if _, err := notify.ParseQuietHours(s.QuietHours); err != nil {
			return &ValidationError{Field: "quiet_hours", Reason: "需要 HH:MM-HH:MM"}
		}
	}
	for _, tc := range []struct {
		field string
		value int
		min   int
	}{
		{"retention_monitor_logs_days", s.RetentionMonitorLogsDays, 0},
		{"retention_balance_snapshots_days", s.RetentionBalanceSnapshotsDays, 0},
		{"retention_notification_logs_days", s.RetentionNotificationLogsDays, 0},
		{"balance_low_cooldown_minutes", s.BalanceLowCooldownMinutes, 0},
		{"failure_cooldown_minutes", s.FailureCooldownMinutes, 0},
		{"forecast_cooldown_minutes", s.ForecastCooldownMinutes, 0},
		{"failure_threshold", s.FailureThreshold, 1},
		{"send_max_attempts", s.SendMaxAttempts, 1},
		{"forecast_window_days", s.ForecastWindowDays, 1},
		{"forecast_alert_days", s.ForecastAlertDays, 0},
	} {
		if tc.value < tc.min {
			return &ValidationError{Field: tc.field, Reason: fmt.Sprintf("需要 >= %d", tc.min)}
		}
	}
	if s.MinChangePct < 0 {
		return &ValidationError{Field: "min_change_pct", Reason: "需要 >= 0"}
	}
	return nil
}

// SchedulerConfigOf 转成 scheduler 期望的配置（Concurrency 不在热更新范围，置 0）。
func SchedulerConfigOf(s storage.AppSettings) config.SchedulerConfig {
	return config.SchedulerConfig{
		BalanceCron: s.BalanceCron,
		RateCron:    s.RateCron,
		Retention: config.RetentionConfig{
			Cron:                 s.RetentionCron,
			MonitorLogsDays:      s.RetentionMonitorLogsDays,
			BalanceSnapshotsDays: s.RetentionBalanceSnapshotsDays,
			NotificationLogsDays: s.RetentionNotificationLogsDays,
		},
	}
}

// PolicyOf 转成 notify.Dispatcher 的去抖策略。QuietHours 已通过 Validate
// 保证合法；这里解析失败静默退化为零值（= 关闭静默），不会让进程带病启动。
// SendMaxAttempts <= 0 由 Dispatcher 归一化为 1（与构造器同语义）。
func PolicyOf(s storage.AppSettings) notify.Policy {
	quiet, err := notify.ParseQuietHours(s.QuietHours)
	if err != nil {
		quiet = notify.QuietHours{}
	}
	return notify.Policy{
		BatchRateChanges:   s.BatchRateChanges,
		MinChangePct:       s.MinChangePct,
		BalanceLowCooldown: minutes(s.BalanceLowCooldownMinutes),
		ForecastCooldown:   minutes(s.ForecastCooldownMinutes),
		FailureCooldown:    minutes(s.FailureCooldownMinutes),
		QuietHours:         quiet,
		SendMaxAttempts:    s.SendMaxAttempts,
	}
}

// MonitorForecastOf 转成 monitor 的预测参数。
func MonitorForecastOf(s storage.AppSettings) monitor.ForecastConfig {
	return monitor.ForecastConfig{
		WindowDays: s.ForecastWindowDays,
		AlertDays:  s.ForecastAlertDays,
	}
}

func minutes(n int) time.Duration {
	return time.Duration(n) * time.Minute
}
