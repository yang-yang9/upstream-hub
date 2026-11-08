// API 集成测试（env 门控 UPSTREAMHUB_TEST_DB=1）：/metrics 端点端到端。
// 对应 spec：docs/specs/metrics-export.md AC-1~6 与软删除边界。Auth 传 nil（/metrics 不走 /api 组）。
package api_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/worryzyy/upstream-hub/internal/api"
	"github.com/worryzyy/upstream-hub/internal/storage"
	"github.com/worryzyy/upstream-hub/internal/storage/storagetest"
	"gorm.io/gorm"
)

func newMetricsRouter(t *testing.T, token string) (*gin.Engine, *gorm.DB, *storage.Channels) {
	t.Helper()
	db := storagetest.Open(t)
	channels := storage.NewChannels(db)
	monLogs := storage.NewMonitorLogs(db)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	api.Register(r, &api.Deps{
		DB:           db,
		Channels:     channels,
		MonLogs:      monLogs,
		MetricsToken: token,
		Log:          slog.Default(),
	})
	return r, db, channels
}

func getMetrics(t *testing.T, r *gin.Engine, auth string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// seedMetricsChannel 建一个开监控、余额 87.5 的 alpha 渠道，并写一条成功的 balance 扫描日志。
func seedMetricsChannel(t *testing.T, ch *storage.Channels, mlog *storage.MonitorLogs) {
	t.Helper()
	c := &storage.Channel{
		Name: "alpha", Type: storage.ChannelTypeNewAPI, SiteURL: "https://a.example",
		Username: "u", MonitorEnabled: true,
	}
	if err := ch.Create(c); err != nil {
		t.Fatalf("Create channel: %v", err)
	}
	if err := ch.UpdateBalance(c.ID, 87.5, time.Now().UTC(), ""); err != nil {
		t.Fatalf("UpdateBalance: %v", err)
	}
	if err := mlog.Append(&storage.MonitorLog{
		ChannelID: c.ID, Job: storage.MonitorJobBalance, Success: true, DurationMS: 1234,
		StartedAt: time.Unix(1731024000, 0).UTC(), FinishedAt: time.Unix(1731024000, 0).UTC(),
	}); err != nil {
		t.Fatalf("Append log: %v", err)
	}
}

// TestMetricsHappyPath AC-1/2/3/4：鉴权通过 → 200 + text format 全指标集；二次抓取计数递增。
func TestMetricsHappyPath(t *testing.T) {
	r, db, channels := newMetricsRouter(t, "secret")
	mlog := storage.NewMonitorLogs(db)
	seedMetricsChannel(t, channels, mlog)

	w := getMetrics(t, r, "Bearer secret")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200，body: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain; version=0.0.4") {
		t.Errorf("Content-Type = %q, want text/plain; version=0.0.4", ct)
	}
	body := w.Body.String()
	for _, line := range []string{
		"upstreamhub_up 1",
		`upstreamhub_build_info{version="`,
		"upstreamhub_channels_total 1",
		`upstreamhub_channel_balance{name="alpha",type="newapi"} 87.5`,
		`upstreamhub_channel_monitor_enabled{name="alpha"} 1`,
		`upstreamhub_channel_last_scan_success{name="alpha",job="balance"} 1`,
		`upstreamhub_channel_last_scan_duration_ms{name="alpha",job="balance"} 1234`,
		`upstreamhub_channel_last_scan_started_at{name="alpha",job="balance"} 1731024000`,
		"upstreamhub_metrics_scrapes_total 1",
		"upstreamhub_metrics_scrape_errors_total 0",
	} {
		if !strings.Contains(body, line) {
			t.Errorf("body 缺少 %q，实际：\n%s", line, body)
		}
	}

	// AC-4：再次成功抓取，scrapes_total 递增到 2。
	w = getMetrics(t, r, "Bearer secret")
	if !strings.Contains(w.Body.String(), "upstreamhub_metrics_scrapes_total 2") {
		t.Errorf("二次抓取后 scrapes_total 应为 2，实际：\n%s", w.Body.String())
	}
}

// TestMetricsAuth AC-5：配置 token 后无头 / 错 token 401 且不计数；未配置 token 时公开。
func TestMetricsAuth(t *testing.T) {
	r, db, channels := newMetricsRouter(t, "secret")
	mlog := storage.NewMonitorLogs(db)
	seedMetricsChannel(t, channels, mlog)

	for _, auth := range []string{"", "Bearer wrong", "Token secret", "secret"} {
		if w := getMetrics(t, r, auth); w.Code != http.StatusUnauthorized {
			t.Errorf("auth %q status = %d, want 401", auth, w.Code)
		}
	}
	// 401 不计数：随后一次成功抓取 scrapes_total 应为 1 而不是 5。
	w := getMetrics(t, r, "bearer secret") // scheme 大小写不敏感
	if w.Code != http.StatusOK {
		t.Fatalf("小写 bearer status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "upstreamhub_metrics_scrapes_total 1") {
		t.Errorf("401 不应计数，scrapes_total 应为 1，实际：\n%s", w.Body.String())
	}

	// 未配置 token：公开（行为同 /healthz）。
	r2, _, _ := newMetricsRouter(t, "")
	if w := getMetrics(t, r2, ""); w.Code != http.StatusOK {
		t.Errorf("未配置 token 时无头 status = %d, want 200", w.Code)
	}
}

// TestMetricsDBError AC-6：monitor_logs 查询失败 → 500 + errors 计数；恢复后 errors_total 可见。
func TestMetricsDBError(t *testing.T) {
	r, db, _ := newMetricsRouter(t, "")

	// 模拟 DB 故障：直接删表让 LatestPerChannel 报错（channels 查询仍成功）。
	if err := db.Migrator().DropTable("monitor_logs"); err != nil {
		t.Fatalf("DropTable: %v", err)
	}
	if w := getMetrics(t, r, ""); w.Code != http.StatusInternalServerError {
		t.Errorf("DB 故障时 status = %d, want 500，body: %s", w.Code, w.Body.String())
	}

	// 恢复表结构后：errors_total 1，且失败那次未计入 scrapes_total。
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	w := getMetrics(t, r, "")
	if w.Code != http.StatusOK {
		t.Fatalf("恢复后 status = %d, want 200", w.Code)
	}
	for _, line := range []string{
		"upstreamhub_metrics_scrapes_total 1",
		"upstreamhub_metrics_scrape_errors_total 1",
	} {
		if !strings.Contains(w.Body.String(), line) {
			t.Errorf("body 缺少 %q，实际：\n%s", line, w.Body.String())
		}
	}
}

// TestMetricsSoftDeletedChannel 边界用例：软删除渠道后渠道系列与 last_scan 系列都消失。
func TestMetricsSoftDeletedChannel(t *testing.T) {
	r, db, channels := newMetricsRouter(t, "")
	mlog := storage.NewMonitorLogs(db)
	seedMetricsChannel(t, channels, mlog)

	list, err := channels.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %v, %v", list, err)
	}
	if err := channels.Delete(list[0].ID); err != nil { // 软删除
		t.Fatalf("Delete: %v", err)
	}

	w := getMetrics(t, r, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, `name="alpha"`) || strings.Contains(body, "job=") {
		t.Errorf("软删除渠道的系列不应出现，实际：\n%s", body)
	}
	if !strings.Contains(body, "upstreamhub_channels_total 0") {
		t.Errorf("软删除后 channels_total 应为 0，实际：\n%s", body)
	}
}

// TestMetricsEmptyDatabase 边界用例：空库 200，只有进程级指标。
func TestMetricsEmptyDatabase(t *testing.T) {
	r, _, _ := newMetricsRouter(t, "")
	w := getMetrics(t, r, "")
	if w.Code != http.StatusOK {
		t.Fatalf("空库 status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "upstreamhub_channels_total 0") {
		t.Errorf("空库应输出 channels_total 0，实际：\n%s", body)
	}
	// 判"无渠道系列"查数据行（name= 标签）：HELP/TYPE 头本身就含 channel_balance 字样。
	if strings.Contains(body, `name="`) {
		t.Errorf("空库不应有渠道系列，实际：\n%s", body)
	}
}
