// API 集成测试（env 门控 UPSTREAMHUB_TEST_DB=1）：/rate-compare 端点走真 PostgreSQL。
// 对应 spec：docs/specs/rate-compare.md AC-3。Auth 传 nil，Monitor 仅占位。
package api_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/worryzyy/upstream-hub/internal/api"
	"github.com/worryzyy/upstream-hub/internal/monitor"
	"github.com/worryzyy/upstream-hub/internal/storage"
	"github.com/worryzyy/upstream-hub/internal/storage/storagetest"
)

func newCompareRouter(t *testing.T) (*gin.Engine, *storage.Channels, *storage.Rates) {
	t.Helper()
	db := storagetest.Open(t)
	channels := storage.NewChannels(db)
	rates := storage.NewRates(db)
	mon := monitor.NewService(channels, rates, nil, nil, nil, slog.Default(), monitor.ForecastConfig{},
		monitor.NewFailureTracker(storage.NewFailures(db), 3, slog.Default()))

	gin.SetMode(gin.TestMode)
	r := gin.New()
	api.Register(r, &api.Deps{
		DB:       db,
		Channels: channels,
		Rates:    rates,
		Monitor:  mon,
		Log:      slog.Default(),
	})
	return r, channels, rates
}

func TestRateCompareEndpoint(t *testing.T) {
	r, channels, rates := newCompareRouter(t)
	now := time.Now().UTC().Truncate(time.Microsecond)

	seed := []struct {
		name string
		typ  storage.ChannelType
	}{
		{"alpha", storage.ChannelTypeNewAPI},
		{"beta", storage.ChannelTypeSub2API},
		{"gamma", storage.ChannelTypeNewAPI},
	}
	ids := map[string]uint{}
	for _, s := range seed {
		ch := &storage.Channel{Name: s.name, Type: s.typ, SiteURL: "https://x.example", Username: "u"}
		if err := channels.Create(ch); err != nil {
			t.Fatalf("create %s: %v", s.name, err)
		}
		ids[s.name] = ch.ID
	}
	mustUpsert := func(channelID uint, model string, ratio float64) {
		t.Helper()
		if _, err := rates.Upsert(&storage.RateSnapshot{
			ChannelID: channelID, ModelName: model, Ratio: ratio, LastSeenAt: now,
		}); err != nil {
			t.Fatalf("upsert %d/%s: %v", channelID, model, err)
		}
	}
	mustUpsert(ids["alpha"], "default", 1.0)
	mustUpsert(ids["alpha"], "vip", 0.8)
	mustUpsert(ids["beta"], "default", 1.2)
	mustUpsert(ids["gamma"], "vip", 0.9)

	get := func(t *testing.T, r *gin.Engine, query string) monitor.RateCompareResult {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/rate-compare"+query, nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s, want 200", w.Code, w.Body.String())
		}
		var out struct {
			Data monitor.RateCompareResult `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return out.Data
	}

	t.Run("default min_channels=2 双视图", func(t *testing.T) {
		d := get(t, r, "")
		if len(d.Channels) != 3 {
			t.Fatalf("channels 长度 = %d, want 3: %+v", len(d.Channels), d.Channels)
		}
		// 最低倍率排行：alpha(0.8) < gamma(0.9) < beta(1.2)。
		wantOrder := []string{"alpha", "gamma", "beta"}
		for i, want := range wantOrder {
			if d.Channels[i].ChannelName != want {
				t.Errorf("排行第 %d = %s, want %s", i+1, d.Channels[i].ChannelName, want)
			}
		}
		if d.Channels[0].MinGroup != "vip" || d.Channels[0].GroupCount != 2 {
			t.Errorf("alpha = %+v, want min @vip, 2 组", d.Channels[0])
		}

		// 分组视图：default（alpha+beta）保留、vip（仅 alpha 或仅 gamma）各只有 1 渠道被过滤。
		if len(d.Groups) != 1 || d.Groups[0].ModelName != "default" {
			t.Fatalf("groups = %+v, want 仅 default 一组", d.Groups)
		}
		g := d.Groups[0]
		if g.BestChannelID != ids["alpha"] || g.SpreadPct != 20.0 {
			t.Errorf("default = best %d spread %v, want alpha / 20.0", g.BestChannelID, g.SpreadPct)
		}
		if len(g.Entries) != 2 || g.Entries[0].ChannelName != "alpha" {
			t.Errorf("default entries = %+v, want alpha 在前", g.Entries)
		}
	})

	t.Run("min_channels=1 保留单渠道分组", func(t *testing.T) {
		d := get(t, r, "?min_channels=1")
		if len(d.Groups) != 2 {
			t.Fatalf("groups 长度 = %d, want 2（default + vip）", len(d.Groups))
		}
	})

	t.Run("min_channels 非法回退 2", func(t *testing.T) {
		for _, q := range []string{"?min_channels=0", "?min_channels=-1", "?min_channels=abc"} {
			d := get(t, r, q)
			if len(d.Groups) != 1 {
				t.Errorf("query %q groups 长度 = %d, want 1（回退 2，vip 过滤）", q, len(d.Groups))
			}
		}
	})

	t.Run("空数据返回空数组", func(t *testing.T) {
		rEmpty, _, _ := newCompareRouter(t) // 全新库（Open 自带清表）
		d := get(t, rEmpty, "")
		if len(d.Channels) != 0 || len(d.Groups) != 0 {
			t.Errorf("空库应得空数组, got %+v", d)
		}
	})
}
