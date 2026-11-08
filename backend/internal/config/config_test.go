package config

import (
	"testing"
)

// 对应 spec：docs/specs/failure-recovery.md AC-6。
// Viper 坑：camelCase mapstructure key 不被 EnvKeyReplacer 映射，
// failureThreshold / failureCooldownMinutes 必须显式 BindEnv 才能被环境变量覆盖。

func TestNotificationsFailureConfigDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Notifications.FailureThreshold != 3 {
		t.Errorf("default FailureThreshold = %d, want 3", cfg.Notifications.FailureThreshold)
	}
	if cfg.Notifications.FailureCooldownMinutes != 60 {
		t.Errorf("default FailureCooldownMinutes = %d, want 60", cfg.Notifications.FailureCooldownMinutes)
	}
}

func TestNotificationsFailureConfigFromEnv(t *testing.T) {
	t.Setenv("UPSTREAMHUB_NOTIFICATIONS_FAILURE_THRESHOLD", "5")
	t.Setenv("UPSTREAMHUB_NOTIFICATIONS_FAILURE_COOLDOWN_MINUTES", "15")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Notifications.FailureThreshold != 5 {
		t.Errorf("FailureThreshold from env = %d, want 5", cfg.Notifications.FailureThreshold)
	}
	if cfg.Notifications.FailureCooldownMinutes != 15 {
		t.Errorf("FailureCooldownMinutes from env = %d, want 15", cfg.Notifications.FailureCooldownMinutes)
	}
}

// 对应 spec：docs/specs/metrics-export.md AC-5。
// metricsToken 是 camelCase key，不被 EnvKeyReplacer 映射，必须显式 BindEnv；
// 默认空 = /metrics 公开。
func TestMetricsTokenFromEnv(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.MetricsToken != "" {
		t.Errorf("default MetricsToken = %q, want empty（公开）", cfg.Server.MetricsToken)
	}

	t.Setenv("UPSTREAMHUB_METRICS_TOKEN", "secret")
	cfg, err = Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.MetricsToken != "secret" {
		t.Errorf("MetricsToken from env = %q, want %q", cfg.Server.MetricsToken, "secret")
	}
}

// 对应 spec：docs/specs/quiet-hours-cooldown.md AC-1 / AC-2。
// quietHours 默认空 = 静默关闭；env 覆盖生效（camelCase key 需显式 BindEnv）；
// 非法格式在 config.Load 就报错（拒绝启动）。
func TestQuietHoursConfig(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Notifications.QuietHours != "" {
		t.Errorf("default QuietHours = %q, want 空（静默关闭）", cfg.Notifications.QuietHours)
	}

	t.Setenv("UPSTREAMHUB_NOTIFICATIONS_QUIETHOURS", "23:00-08:00")
	cfg, err = Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Notifications.QuietHours != "23:00-08:00" {
		t.Errorf("QuietHours from env = %q, want %q", cfg.Notifications.QuietHours, "23:00-08:00")
	}

	t.Setenv("UPSTREAMHUB_NOTIFICATIONS_QUIETHOURS", "25:00-06:00")
	if _, err := Load(""); err == nil {
		t.Error("Load with 25:00-06:00 = nil error, want 拒绝启动")
	}
}
