// forecast.go 余额消耗速率与到期预测的纯函数，脱离 DB 单测。
// 对应 spec：docs/specs/usage-forecast.md。
package monitor

import (
	"fmt"
	"time"

	"github.com/worryzyy/upstream-hub/internal/notify"
	"github.com/worryzyy/upstream-hub/internal/storage"
)

// ForecastConfig 消耗预测配置（main 接线注入）。AlertDays <= 0 表示告警整体关闭。
type ForecastConfig struct {
	// WindowDays 回归窗口（天）：只用最近 N 天的快照参与回归。
	WindowDays int
	// AlertDays 预计耗尽天数 <= 该值时发 balance_forecast_low 告警；0 = 关闭。
	AlertDays int
}

// BurnForecast 线性回归得出的余额消耗预测。Depleting=false 时数值字段为零值。
type BurnForecast struct {
	ChannelID  uint       `json:"channel_id"`
	DailyBurn  float64    `json:"daily_burn"` // 日均消耗（正数）
	Latest     float64    `json:"latest"`     // 最新样本余额
	DaysLeft   float64    `json:"days_left"`  // Latest / DailyBurn，可为 0 或负
	DepletedAt *time.Time `json:"depleted_at"`
	Samples    int        `json:"samples"`    // 实际参与回归的样本数
	WindowDays int        `json:"window_days"`
	Depleting  bool       `json:"depleting"`
}

// forecastBurn 对窗口内的余额快照做最小二乘线性回归（x = Unix 秒，y = 余额），
// 返回日均消耗与外推的耗尽时点。
//
// 不预测（ok=false）的情况：窗口内样本 < 2、斜率 >= 0（持平 / 上升，含充值）、
// 全部样本时间相同（无法回归）。充值跳变会把斜率拉平，导致漏报而非误报——
// 接受该偏差，剔除跳变见 spec 非目标。
func forecastBurn(samples []storage.BalanceSnapshot, now time.Time, windowDays int) (BurnForecast, bool) {
	if windowDays <= 0 {
		windowDays = 7
	}
	cutoff := now.Add(-time.Duration(windowDays) * 24 * time.Hour)

	var xs, ys []float64
	latestAt := time.Time{}
	var latestBal float64
	for _, s := range samples {
		if s.SampledAt.Before(cutoff) || s.SampledAt.After(now) {
			continue
		}
		xs = append(xs, float64(s.SampledAt.Unix()))
		ys = append(ys, s.Balance)
		if s.SampledAt.After(latestAt) {
			latestAt, latestBal = s.SampledAt, s.Balance
		}
	}
	base := BurnForecast{WindowDays: windowDays, Samples: len(xs)}
	if len(xs) < 2 {
		return base, false
	}

	// 最小二乘斜率：Sxy / Sxx。Sxx == 0 说明所有样本时间相同，无法回归。
	var sumX, sumY float64
	for i := range xs {
		sumX += xs[i]
		sumY += ys[i]
	}
	meanX, meanY := sumX/float64(len(xs)), sumY/float64(len(ys))
	var sxy, sxx float64
	for i := range xs {
		sxy += (xs[i] - meanX) * (ys[i] - meanY)
		sxx += (xs[i] - meanX) * (xs[i] - meanX)
	}
	if sxx == 0 {
		return base, false
	}
	slope := sxy / sxx
	dailyBurn := -slope * 86400
	if dailyBurn <= 0 {
		return base, false
	}

	daysLeft := latestBal / dailyBurn
	depletedAt := latestAt.Add(time.Duration(daysLeft * 24 * float64(time.Hour)))
	return BurnForecast{
		DailyBurn:  dailyBurn,
		Latest:     latestBal,
		DaysLeft:   daysLeft,
		DepletedAt: &depletedAt,
		Samples:    len(xs),
		WindowDays: windowDays,
		Depleting:  true,
	}, true
}

// balanceForecastMessage 判断预测是否命中告警阈值并构造消息。
// alertDays <= 0（未配置）或非消耗状态或 DaysLeft > alertDays 时不触发。
func balanceForecastMessage(c *storage.Channel, f BurnForecast, alertDays int) (notify.Message, bool) {
	if alertDays <= 0 || !f.Depleting || f.DaysLeft > float64(alertDays) {
		return notify.Message{}, false
	}
	subject := fmt.Sprintf("[upstream-hub] %s 预计 %.1f 天后余额耗尽", c.Name, f.DaysLeft)
	if f.DaysLeft <= 0 {
		subject = fmt.Sprintf("[upstream-hub] %s 余额已耗尽", c.Name)
	}
	return notify.Message{
		Event:     storage.EventBalanceForecast,
		ChannelID: c.ID,
		Subject:   subject,
		Body: fmt.Sprintf("当前余额: %.4f，日均消耗: %.4f，预计耗尽时间: %s",
			f.Latest, f.DailyBurn, f.DepletedAt.UTC().Format("2006-01-02")),
	}, true
}
