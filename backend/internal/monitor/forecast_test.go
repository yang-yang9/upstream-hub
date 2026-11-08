package monitor

import (
	"strings"
	"testing"
	"time"

	"github.com/worryzyy/upstream-hub/internal/storage"
)

// TDD red：forecastBurn / balanceForecastMessage 尚不存在（将在 monitor/forecast.go 实现）。
// 对应 spec：docs/specs/usage-forecast.md。

// linearDecline 生成一条线性下降的余额序列：从 balance0 开始，每 step 耗 burn。
func linearDecline(start time.Time, balance0, burn float64, step time.Duration, n int) []storage.BalanceSnapshot {
	out := make([]storage.BalanceSnapshot, n)
	for i := 0; i < n; i++ {
		out[i] = storage.BalanceSnapshot{
			ChannelID: 7,
			Balance:   balance0 - burn*float64(i),
			SampledAt: start.Add(step * time.Duration(i)),
		}
	}
	return out
}

func TestForecastBurn(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name           string
		samples        []storage.BalanceSnapshot
		now            time.Time
		windowDays     int
		wantOK         bool
		wantDaily      float64
		wantDays       float64
		wantSamples    int
		wantDepletedAt time.Time
	}{
		{
			// 每 12h 掉 10：日均 20，最新余额 70 → 3.5 天后耗尽。
			name:           "linear decline",
			samples:        linearDecline(t0, 100, 10, 12*time.Hour, 4),
			now:            t0.Add(37 * time.Hour),
			windowDays:     7,
			wantOK:         true,
			wantDaily:      20,
			wantDays:       3.5,
			wantSamples:    4,
			wantDepletedAt: t0.Add(36 * time.Hour).Add(84 * time.Hour),
		},
		{
			// 窗口外（8 天前）的样本被剔除，不参与回归。
			name: "old samples filtered out",
			samples: append(
				linearDecline(t0.Add(-8*24*time.Hour), 200, 10, 12*time.Hour, 2),
				linearDecline(t0, 100, 10, 12*time.Hour, 3)...,
			),
			now:            t0.Add(30 * time.Hour),
			windowDays:     7,
			wantOK:         true,
			wantDaily:      20,
			wantDays:       4,
			wantSamples:    3,
			wantDepletedAt: t0.Add(24 * time.Hour).Add(96 * time.Hour),
		},
		{
			// 剔除后只剩 1 个样本 → 无预测。
			name: "single sample in window",
			samples: []storage.BalanceSnapshot{
				{ChannelID: 7, Balance: 100, SampledAt: t0.Add(-8 * 24 * time.Hour)},
				{ChannelID: 7, Balance: 80, SampledAt: t0},
			},
			now:        t0.Add(time.Hour),
			windowDays: 7,
			wantOK:     false,
		},
		{
			name:       "no samples at all",
			samples:    nil,
			now:        t0,
			windowDays: 7,
			wantOK:     false,
		},
		{
			// 余额持平：斜率 0，不会耗尽。
			name:        "flat balance not depleting",
			samples:     linearDecline(t0, 100, 0, 12*time.Hour, 4),
			now:         t0.Add(36 * time.Hour),
			windowDays:  7,
			wantOK:      false,
			wantSamples: 4,
		},
		{
			// 余额上升（充值）：斜率为正，不预测。
			name:        "rising balance not depleting",
			samples:     linearDecline(t0, 100, -5, 12*time.Hour, 4),
			now:         t0.Add(36 * time.Hour),
			windowDays:  7,
			wantOK:      false,
			wantSamples: 4,
		},
		{
			// 最新余额已经见底：预测照常产出，DaysLeft = 0。
			name:           "depleted balance",
			samples:        linearDecline(t0, 30, 10, 12*time.Hour, 4),
			now:            t0.Add(37 * time.Hour),
			windowDays:     7,
			wantOK:         true,
			wantDaily:      20,
			wantDays:       0,
			wantSamples:    4,
			wantDepletedAt: t0.Add(36 * time.Hour),
		},
	} {
		f, ok := forecastBurn(tc.samples, tc.now, tc.windowDays)
		if ok != tc.wantOK {
			t.Errorf("%s: ok = %v, want %v", tc.name, ok, tc.wantOK)
			continue
		}
		if !tc.wantOK {
			continue
		}
		if diff := absDiff(f.DailyBurn, tc.wantDaily); diff > 1e-9 {
			t.Errorf("%s: DailyBurn = %v, want %v", tc.name, f.DailyBurn, tc.wantDaily)
		}
		if diff := absDiff(f.DaysLeft, tc.wantDays); diff > 1e-9 {
			t.Errorf("%s: DaysLeft = %v, want %v", tc.name, f.DaysLeft, tc.wantDays)
		}
		if f.Samples != tc.wantSamples {
			t.Errorf("%s: Samples = %d, want %d", tc.name, f.Samples, tc.wantSamples)
		}
		if f.WindowDays != tc.windowDays {
			t.Errorf("%s: WindowDays = %d, want %d", tc.name, f.WindowDays, tc.windowDays)
		}
		if !f.DepletedAt.Equal(tc.wantDepletedAt) {
			t.Errorf("%s: DepletedAt = %v, want %v", tc.name, f.DepletedAt, tc.wantDepletedAt)
		}
	}
}

func TestBalanceForecastMessage(t *testing.T) {
	c := &storage.Channel{ID: 7, Name: "alpha"}
	latestAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name      string
		daysLeft  float64
		alertDays int
		wantOK    bool
	}{
		{"below threshold alerts", 3.5, 7, true},
		{"exactly at threshold alerts", 7, 7, true}, // 恰好等于阈值：告警
		{"above threshold no alert", 10, 7, false},  // 阈值之外
		{"alert disabled", 3.5, 0, false},           // 告警关闭
		{"already depleted alerts", -1, 7, true},    // 已耗尽：仍然告警
	} {
		depletedAt := latestAt.Add(time.Duration(tc.daysLeft * 24 * float64(time.Hour)))
		f := BurnForecast{
			DailyBurn:  20,
			Latest:     70,
			DaysLeft:   tc.daysLeft,
			DepletedAt: &depletedAt,
			Depleting:  true,
			Samples:    4,
			WindowDays: 7,
		}
		msg, ok := balanceForecastMessage(c, f, tc.alertDays)
		if ok != tc.wantOK {
			t.Errorf("%s: ok = %v, want %v", tc.name, ok, tc.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if msg.Event != storage.EventBalanceForecast {
			t.Errorf("%s: Event = %v, want %v", tc.name, msg.Event, storage.EventBalanceForecast)
		}
		if msg.ChannelID != c.ID {
			t.Errorf("%s: ChannelID = %d, want %d", tc.name, msg.ChannelID, c.ID)
		}
		if !strings.Contains(msg.Subject, "alpha") || !strings.Contains(msg.Subject, "耗尽") {
			t.Errorf("%s: Subject = %q, want 含渠道名与耗尽字样", tc.name, msg.Subject)
		}
		for _, want := range []string{"日均消耗", "余额"} {
			if !strings.Contains(msg.Body, want) {
				t.Errorf("%s: Body = %q, want 含 %q", tc.name, msg.Body, want)
			}
		}
	}
}

func absDiff(a, b float64) float64 {
	d := a - b
	if d < 0 {
		return -d
	}
	return d
}
