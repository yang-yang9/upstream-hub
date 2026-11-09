package api_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/worryzyy/upstream-hub/internal/api"
	"github.com/worryzyy/upstream-hub/internal/config"
	"github.com/worryzyy/upstream-hub/internal/settings"
	"github.com/worryzyy/upstream-hub/internal/storage"
	"github.com/worryzyy/upstream-hub/internal/storage/storagetest"
)

// TDD red：GET / PUT / DELETE /api/settings 尚不存在（将在 internal/api/settings.go
// 实现）。对应 spec：docs/specs/runtime-settings.md AC-1/3/4/5/7。
// env 门控（UPSTREAMHUB_TEST_DB=1）：settings.Service 落库依赖真 PostgreSQL。

func newSettingsRouter(t *testing.T) *gin.Engine {
	t.Helper()
	db := storagetest.Open(t)
	base := settings.FromConfig(&config.Config{
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
			QuietHours:                "",
			SendMaxAttempts:           3,
			BalanceRecovery:           false,
		},
		Forecast: config.ForecastConfig{WindowDays: 7, AlertDays: 3, CooldownMinutes: 720},
	})
	svc := settings.NewService(storage.NewAppSettings(db), base)
	if err := svc.Init(); err != nil {
		t.Fatalf("settings Init: %v", err)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api.Register(r, &api.Deps{DB: db, Settings: svc, Log: slog.Default()})
	return r
}

func getSettingsData(t *testing.T, r *gin.Engine) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/settings = %d, want 200（body %s）", w.Code, w.Body.String())
	}
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("解析响应: %v（body %s）", err, w.Body.String())
	}
	return env.Data
}

// fullSettingsBody 提交全量 17 字段（UI 语义：无部分更新）。
func fullSettingsBody() map[string]any {
	return map[string]any{
		"balance_cron":                    "0 0 5 * * *",
		"rate_cron":                       "0 30 5 * * *",
		"retention_cron":                  "0 17 3 * * *",
		"retention_monitor_logs_days":     14,
		"retention_balance_snapshots_days": 60,
		"retention_notification_logs_days": 60,
		"batch_rate_changes":              false,
		"min_change_pct":                  1.5,
		"balance_low_cooldown_minutes":    120,
		"failure_threshold":               5,
		"failure_cooldown_minutes":        30,
		"quiet_hours":                     "23:00-07:00",
		"send_max_attempts":               2,
		"balance_recovery":                true,
		"forecast_window_days":            10,
		"forecast_alert_days":             4,
		"forecast_cooldown_minutes":       360,
	}
}

func putSettings(t *testing.T, r *gin.Engine, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestSettingsGetDefaults(t *testing.T) {
	r := newSettingsRouter(t)
	data := getSettingsData(t, r)

	if data["balance_cron"] != "37 */15 * * * *" {
		t.Errorf("balance_cron = %v, want env 推导值", data["balance_cron"])
	}
	if data["failure_threshold"] != float64(3) {
		t.Errorf("failure_threshold = %v, want 3", data["failure_threshold"])
	}
	if data["quiet_hours"] != "" {
		t.Errorf("quiet_hours = %v, want \"\"", data["quiet_hours"])
	}
	if data["saved"] != false {
		t.Errorf("saved = %v, want false（无行）", data["saved"])
	}
}

func TestSettingsPutValid(t *testing.T) {
	r := newSettingsRouter(t)
	body := fullSettingsBody()

	w := putSettings(t, r, body)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT = %d, want 200（body %s）", w.Code, w.Body.String())
	}
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("解析 PUT 响应: %v", err)
	}
	if env.Data["saved"] != true {
		t.Errorf("PUT 响应 saved = %v, want true", env.Data["saved"])
	}
	if env.Data["failure_threshold"] != float64(5) {
		t.Errorf("PUT 响应 failure_threshold = %v, want 5", env.Data["failure_threshold"])
	}

	// GET 反映落库值（库优先，env 被忽略）。
	data := getSettingsData(t, r)
	if data["balance_cron"] != "0 0 5 * * *" {
		t.Errorf("GET balance_cron = %v, want 保存值", data["balance_cron"])
	}
	if data["quiet_hours"] != "23:00-07:00" {
		t.Errorf("GET quiet_hours = %v, want 保存值", data["quiet_hours"])
	}
	if data["saved"] != true {
		t.Errorf("GET saved = %v, want true", data["saved"])
	}
	if _, ok := data["updated_at"]; !ok {
		t.Error("GET 应带 updated_at")
	}
}

func TestSettingsPutInvalid(t *testing.T) {
	r := newSettingsRouter(t)

	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"5 段 cron（缺秒）", func() map[string]any {
			b := fullSettingsBody()
			b["balance_cron"] = "*/15 * * * *"
			return b
		}()},
		{"非法 quiet_hours", func() map[string]any {
			b := fullSettingsBody()
			b["quiet_hours"] = "25:00-07:00"
			return b
		}()},
		{"failure_threshold 0", func() map[string]any {
			b := fullSettingsBody()
			b["failure_threshold"] = 0
			return b
		}()},
		{"负保留天数", func() map[string]any {
			b := fullSettingsBody()
			b["retention_monitor_logs_days"] = -1
			return b
		}()},
	} {
		w := putSettings(t, r, tc.body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: PUT = %d, want 400（body %s）", tc.name, w.Code, w.Body.String())
		}
	}

	// 非法输入不落库：GET 仍是 env 推导值。
	data := getSettingsData(t, r)
	if data["saved"] != false {
		t.Errorf("非法 PUT 后 saved = %v, want false（不落库）", data["saved"])
	}
	if data["balance_cron"] != "37 */15 * * * *" {
		t.Errorf("非法 PUT 后 balance_cron = %v, want 原值", data["balance_cron"])
	}
}

// AC-7：范围外字段（server.port 等）出现在请求体中被忽略，不影响行为。
func TestSettingsPutIgnoresOutOfScopeFields(t *testing.T) {
	r := newSettingsRouter(t)
	body := fullSettingsBody()
	body["port"] = 9999
	body["concurrency"] = 64
	body["password"] = "hacked"

	w := putSettings(t, r, body)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT 含范围外字段 = %d, want 200（body %s）", w.Code, w.Body.String())
	}
	data := getSettingsData(t, r)
	if data["balance_cron"] != "0 0 5 * * *" {
		t.Errorf("范围外字段不应影响合法字段保存，balance_cron = %v", data["balance_cron"])
	}
}

func TestSettingsDeleteResets(t *testing.T) {
	r := newSettingsRouter(t)
	if w := putSettings(t, r, fullSettingsBody()); w.Code != http.StatusOK {
		t.Fatalf("PUT = %d, want 200", w.Code)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/settings", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE = %d, want 200（body %s）", w.Code, w.Body.String())
	}

	// 等价于"从未保存过"：回到 env 推导值。
	data := getSettingsData(t, r)
	if data["saved"] != false {
		t.Errorf("DELETE 后 saved = %v, want false", data["saved"])
	}
	if data["balance_cron"] != "37 */15 * * * *" {
		t.Errorf("DELETE 后 balance_cron = %v, want env 推导值", data["balance_cron"])
	}
}
