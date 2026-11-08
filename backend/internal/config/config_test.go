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
