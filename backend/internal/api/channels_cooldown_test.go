// API 集成测试（env 门控 UPSTREAMHUB_TEST_DB=1）：渠道 notify_cooldowns 字段的
// 校验（非法 JSON / 未知键 / 负值 → 400）与落库回读。
// 对应 spec：docs/specs/quiet-hours-cooldown.md AC-4。Auth 传 nil。
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
	"github.com/worryzyy/upstream-hub/internal/channel"
	"github.com/worryzyy/upstream-hub/internal/crypto"
	"github.com/worryzyy/upstream-hub/internal/storage"
	"github.com/worryzyy/upstream-hub/internal/storage/storagetest"
	"gorm.io/gorm"
)

func newChannelRouter(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	db := storagetest.Open(t)
	channels := storage.NewChannels(db)
	cipher, err := crypto.NewCipher("test-only-key")
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	channelSvc := channel.NewService(
		channels, storage.NewAuthSessions(db), storage.NewCaptchas(db),
		storage.NewMonitorLogs(db), cipher)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	api.Register(r, &api.Deps{
		DB:         db,
		Channels:   channels,
		ChannelSvc: channelSvc,
		Log:        slog.Default(),
	})
	return r, db
}

func postChannel(t *testing.T, r *gin.Engine, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/channels", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func getChannelField(t *testing.T, r *gin.Engine, id uint) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/channels/1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET channel: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Data struct {
			NotifyCooldowns string `json:"notify_cooldowns"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out.Data.NotifyCooldowns
}

func baseChannelBody() map[string]any {
	return map[string]any{
		"name":     "alpha",
		"type":     "newapi",
		"site_url": "https://x.example",
		"username": "u",
		"password": "pw",
	}
}

func TestChannelNotifyCooldowns(t *testing.T) {
	r, _ := newChannelRouter(t)

	// 合法覆盖：落库并原样回读。
	body := baseChannelBody()
	body["notify_cooldowns"] = `{"balance_low":10,"monitor_failed":5}`
	w := postChannel(t, r, body)
	if w.Code != http.StatusOK {
		t.Fatalf("create with valid cooldowns: %d %s", w.Code, w.Body.String())
	}
	if got := getChannelField(t, r, 1); got != `{"balance_low":10,"monitor_failed":5}` {
		t.Errorf("notify_cooldowns 回读 = %q, want 原样", got)
	}

	// 非法输入全部 400。
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"not json", "not json"},
		{"unknown event", `{"rate_changed":10}`},
		{"negative", `{"balance_low":-5}`},
		{"fractional", `{"balance_low":1.5}`},
		{"string value", `{"balance_low":"10"}`},
	} {
		body := baseChannelBody()
		body["name"] = "bad-" + tc.name
		body["notify_cooldowns"] = tc.raw
		w := postChannel(t, r, body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("create with %s: status = %d, want 400（body: %s）", tc.name, w.Code, w.Body.String())
		}
	}

	// 省略字段 = 不覆盖，落空串。
	body = baseChannelBody()
	body["name"] = "beta"
	w = postChannel(t, r, body)
	if w.Code != http.StatusOK {
		t.Fatalf("create without cooldowns: %d %s", w.Code, w.Body.String())
	}
}

func TestChannelNotifyCooldownsUpdate(t *testing.T) {
	r, _ := newChannelRouter(t)

	body := baseChannelBody()
	body["notify_cooldowns"] = `{"balance_low":10}`
	if w := postChannel(t, r, body); w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}

	// 更新为空串：清掉覆盖。
	raw, _ := json.Marshal(map[string]any{"notify_cooldowns": ""})
	req := httptest.NewRequest(http.MethodPut, "/api/channels/1", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update with empty cooldowns: %d %s", w.Code, w.Body.String())
	}
	if got := getChannelField(t, r, 1); got != "" {
		t.Errorf("清空后 notify_cooldowns = %q, want 空", got)
	}

	// 更新为非法 JSON：400。
	raw, _ = json.Marshal(map[string]any{"notify_cooldowns": "not json"})
	req = httptest.NewRequest(http.MethodPut, "/api/channels/1", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("update with invalid cooldowns: status = %d, want 400", w.Code)
	}
	// 坏更新不落库。
	if got := getChannelField(t, r, 1); got != "" {
		t.Errorf("坏更新后 notify_cooldowns = %q, want 保持空", got)
	}
}
