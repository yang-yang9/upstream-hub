// API 集成测试（env 门控 UPSTREAMHUB_TEST_DB=1）：forecast 端点走真 PostgreSQL。
// 对应 spec：docs/specs/usage-forecast.md AC-5。Auth 传 nil（测试不带鉴权中间件），
// monitor.Service 只用到 Rates / ForecastConfig，其余依赖传零值即可。
package api_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"fmt"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/worryzyy/upstream-hub/internal/api"
	"github.com/worryzyy/upstream-hub/internal/monitor"
	"github.com/worryzyy/upstream-hub/internal/storage"
	"github.com/worryzyy/upstream-hub/internal/storage/storagetest"
)

func newForecastRouter(t *testing.T) (*gin.Engine, *storage.Channels, *storage.Rates) {
	t.Helper()
	db := storagetest.Open(t)
	channels := storage.NewChannels(db)
	rates := storage.NewRates(db)
	mon := monitor.NewService(channels, rates, nil, nil, nil, slog.Default(), monitor.ForecastConfig{WindowDays: 7},
		monitor.NewFailureTracker(storage.NewFailures(db), 3, slog.Default()), false)

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

func TestForecastEndpoint(t *testing.T) {
	r, channels, rates := newForecastRouter(t)
	now := time.Now().UTC().Truncate(time.Microsecond)

	// alpha：每 12h 掉 10 → 日均 20，最新余额 70。
	alpha := &storage.Channel{Name: "alpha", Type: storage.ChannelTypeNewAPI, SiteURL: "https://a.example", Username: "u"}
	if err := channels.Create(alpha); err != nil {
		t.Fatalf("create alpha: %v", err)
	}
	for i := 0; i < 4; i++ {
		if err := rates.AppendBalance(&storage.BalanceSnapshot{
			ChannelID: alpha.ID,
			Balance:   100 - 10*float64(i),
			SampledAt: now.Add(time.Duration(i-3) * 12 * time.Hour),
		}); err != nil {
			t.Fatalf("seed alpha: %v", err)
		}
	}

	// beta：余额持平 → 非消耗。
	beta := &storage.Channel{Name: "beta", Type: storage.ChannelTypeNewAPI, SiteURL: "https://b.example", Username: "u"}
	if err := channels.Create(beta); err != nil {
		t.Fatalf("create beta: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := rates.AppendBalance(&storage.BalanceSnapshot{
			ChannelID: beta.ID,
			Balance:   50,
			SampledAt: now.Add(time.Duration(i-2) * 12 * time.Hour),
		}); err != nil {
			t.Fatalf("seed beta: %v", err)
		}
	}

	t.Run("depleting channel", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/channels/%d/forecast", alpha.ID), nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s, want 200", w.Code, w.Body.String())
		}
		var out struct {
			Data monitor.BurnForecast `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		d := out.Data
		if !d.Depleting || d.DailyBurn != 20 || d.DaysLeft != 3.5 || d.Samples != 4 {
			t.Errorf("forecast = %+v, want depleting, daily 20, days 3.5, samples 4", d)
		}
	})

	t.Run("flat channel not depleting", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/channels/%d/forecast", beta.ID), nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s, want 200", w.Code, w.Body.String())
		}
		var out struct {
			Data monitor.BurnForecast `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if out.Data.Depleting || out.Data.DailyBurn != 0 {
			t.Errorf("forecast = %+v, want not depleting", out.Data)
		}
	})

	t.Run("missing channel 404", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/channels/9999/forecast", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})
}
