// Package monitor 周期性扫描渠道，采集余额 / 倍率并写入快照、变化日志和通知。
package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/worryzyy/upstream-hub/internal/channel"
	"github.com/worryzyy/upstream-hub/internal/connector"
	"github.com/worryzyy/upstream-hub/internal/notify"
	"github.com/worryzyy/upstream-hub/internal/progress"
	"github.com/worryzyy/upstream-hub/internal/storage"
)

// Service 监控扫描服务。
type Service struct {
	channels    *storage.Channels
	rates       *storage.Rates
	monitorLogs *storage.MonitorLogs
	channelSvc  *channel.Service
	dispatcher  *notify.Dispatcher
	log         *slog.Logger
	forecast    ForecastConfig
	failures    *FailureTracker
}

func NewService(
	channels *storage.Channels,
	rates *storage.Rates,
	monitorLogs *storage.MonitorLogs,
	channelSvc *channel.Service,
	dispatcher *notify.Dispatcher,
	log *slog.Logger,
	forecast ForecastConfig,
	failures *FailureTracker,
) *Service {
	return &Service{
		channels:    channels,
		rates:       rates,
		monitorLogs: monitorLogs,
		channelSvc:  channelSvc,
		dispatcher:  dispatcher,
		log:         log,
		forecast:    forecast,
		failures:    failures,
	}
}

// ScanAllBalances 扫描所有启用监控的渠道余额。单个失败不影响其他。
func (s *Service) ScanAllBalances(ctx context.Context) {
	list, err := s.channels.ListMonitorEnabled()
	if err != nil {
		s.log.Error("list channels", "err", err)
		return
	}
	for i := range list {
		c := list[i]
		if err := s.RefreshBalance(ctx, &c); err != nil {
			s.log.Warn("refresh balance failed", "channel", c.Name, "err", err)
		}
	}
}

// ScanAllRates 扫描所有启用监控的渠道倍率。
func (s *Service) ScanAllRates(ctx context.Context) {
	list, err := s.channels.ListMonitorEnabled()
	if err != nil {
		s.log.Error("list channels", "err", err)
		return
	}
	for i := range list {
		c := list[i]
		if err := s.RefreshRates(ctx, &c); err != nil {
			s.log.Warn("refresh rates failed", "channel", c.Name, "err", err)
		}
	}
}

// RefreshBalance 单个渠道余额刷新，可被 API 手动触发。
// 失败 / 恢复通知走 FailureTracker（连续 N 次才告警 + 恢复通知，
// 对应 spec：docs/specs/failure-recovery.md）。
func (s *Service) RefreshBalance(ctx context.Context, c *storage.Channel) error {
	resolved, conn, session, err := s.prepare(ctx, c)
	if err != nil {
		s.notifyFailure(ctx, c, storage.MonitorJobLogin, err)
		return err
	}
	// prepare 成功（登录 / 会话正常）即 login job 的恢复点（spec AC-4）。
	s.notifyRecovery(ctx, c, storage.MonitorJobLogin)

	progress.Start(ctx, progress.StageBalance, "拉取余额…")
	started := time.Now()
	res, err := conn.GetBalance(ctx, resolved, session)
	finished := time.Now()
	_ = s.monitorLogs.Append(&storage.MonitorLog{
		ChannelID:    c.ID,
		Job:          storage.MonitorJobBalance,
		Success:      err == nil,
		ErrorMessage: errString(err),
		StartedAt:    started,
		FinishedAt:   finished,
	})
	if err != nil {
		progress.Fail(ctx, progress.StageBalance, err.Error())
		s.notifyFailure(ctx, c, storage.MonitorJobBalance, err)
		return err
	}

	sampledAt := res.SampledAt
	if sampledAt.IsZero() {
		sampledAt = time.Now()
	}
	if err := s.channels.UpdateBalance(c.ID, res.Balance, &sampledAt, ""); err != nil {
		return err
	}
	_ = s.rates.AppendBalance(&storage.BalanceSnapshot{
		ChannelID: c.ID,
		Balance:   res.Balance,
		SampledAt: sampledAt,
	})
	progress.OK(ctx, progress.StageBalance, fmt.Sprintf("当前余额 %.4f", res.Balance),
		map[string]any{"balance": res.Balance})

	s.notifyRecovery(ctx, c, storage.MonitorJobBalance)
	if msg, ok := balanceLowMessage(c, res.Balance); ok {
		_ = s.dispatcher.Dispatch(ctx, msg)
	}
	s.dispatchForecast(ctx, c)
	return nil
}

// Forecast 现算某渠道的余额消耗预测（不落库，快照即真相）。
// ok=false 表示无法预测（样本不足 / 非消耗状态）。
func (s *Service) Forecast(channelID uint) (BurnForecast, bool, error) {
	windowDays := s.forecast.WindowDays
	if windowDays <= 0 {
		windowDays = 7
	}
	now := time.Now().UTC()
	since := now.Add(-time.Duration(windowDays) * 24 * time.Hour)
	samples, err := s.rates.ListBalanceSince(channelID, since)
	if err != nil {
		return BurnForecast{ChannelID: channelID, WindowDays: windowDays}, false, err
	}
	f, ok := forecastBurn(samples, now, windowDays)
	f.ChannelID = channelID
	return f, ok, nil
}

// dispatchForecast 余额扫描尾部：预测临近耗尽时发 balance_forecast_low。
// 计算或发送失败只写日志，不影响扫描本身（对应 spec：docs/specs/usage-forecast.md AC-6）。
func (s *Service) dispatchForecast(ctx context.Context, c *storage.Channel) {
	if s.forecast.AlertDays <= 0 {
		return
	}
	f, ok, err := s.Forecast(c.ID)
	if err != nil {
		s.log.Warn("forecast balance failed", "channel", c.Name, "err", err)
		return
	}
	if !ok {
		return
	}
	if msg, ok := balanceForecastMessage(c, f, s.forecast.AlertDays); ok {
		if err := s.dispatcher.Dispatch(ctx, msg); err != nil {
			s.log.Warn("dispatch forecast notification failed", "channel", c.Name, "err", err)
		}
	}
}

// RefreshRates 单个渠道倍率刷新，可被 API 手动触发。
// 失败 / 恢复通知走 FailureTracker（连续 N 次才告警 + 恢复通知，
// 对应 spec：docs/specs/failure-recovery.md）。
func (s *Service) RefreshRates(ctx context.Context, c *storage.Channel) error {
	resolved, conn, session, err := s.prepare(ctx, c)
	if err != nil {
		s.notifyFailure(ctx, c, storage.MonitorJobLogin, err)
		return err
	}
	// prepare 成功即 login job 的恢复点（spec AC-4）。
	s.notifyRecovery(ctx, c, storage.MonitorJobLogin)

	progress.Start(ctx, progress.StageRates, "拉取分组倍率…")
	started := time.Now()
	results, err := conn.GetRates(ctx, resolved, session)
	finished := time.Now()
	_ = s.monitorLogs.Append(&storage.MonitorLog{
		ChannelID:    c.ID,
		Job:          storage.MonitorJobRates,
		Success:      err == nil,
		ErrorMessage: errString(err),
		StartedAt:    started,
		FinishedAt:   finished,
	})
	if err != nil {
		progress.Fail(ctx, progress.StageRates, err.Error())
		s.notifyFailure(ctx, c, storage.MonitorJobRates, err)
		return err
	}

	now := time.Now()
	changes := make([]notify.RateChange, 0, len(results))
	for _, r := range results {
		prev, err := s.rates.Upsert(&storage.RateSnapshot{
			ChannelID:       c.ID,
			ModelName:       r.ModelName,
			Description:     r.Description,
			Ratio:           r.Ratio,
			CompletionRatio: r.CompletionRatio,
			LastSeenAt:      now,
		})
		if err != nil {
			s.log.Warn("rate upsert failed", "channel", c.Name, "model", r.ModelName, "err", err)
			continue
		}
		log, change, changed := rateChangeDiff(c, r.ModelName, prev, r.Ratio, r.CompletionRatio, now)
		if !changed {
			continue
		}
		_ = s.rates.AppendChange(log)
		changes = append(changes, change)
	}
	// 一次扫描的所有变化打包推送：去抖策略（合并 / 涨跌幅过滤）由 Dispatcher.Policy 决定。
	if len(changes) > 0 {
		_ = s.dispatcher.DispatchRateBatch(ctx, c, changes)
	}
	progress.OK(ctx, progress.StageRates, fmt.Sprintf("拉到 %d 个分组", len(results)),
		map[string]any{"count": len(results)})
	s.notifyRecovery(ctx, c, storage.MonitorJobRates)
	return nil
}

func (s *Service) prepare(ctx context.Context, c *storage.Channel) (*connector.Channel, connector.Connector, *connector.AuthSession, error) {
	resolved, err := s.channelSvc.Resolve(ctx, c)
	if err != nil {
		return nil, nil, nil, err
	}
	conn, err := connector.For(resolved.Type)
	if err != nil {
		return nil, nil, nil, err
	}
	session, err := s.channelSvc.EnsureSession(ctx, c, resolved, conn)
	if err != nil {
		return nil, nil, nil, err
	}
	return resolved, conn, session, nil
}

// notifyFailure 失败路径：记录连续失败计数，达阈值才派发告警（spec AC-3）。
// streak 存储出错时 tracker fail-open 照发；重发节流由 Dispatcher 的失败冷却负责。
func (s *Service) notifyFailure(ctx context.Context, c *storage.Channel, job storage.MonitorJob, err error) {
	if m := s.failures.OnFailure(*c, job, err); m != nil {
		if derr := s.dispatcher.Dispatch(ctx, *m); derr != nil {
			s.log.Warn("dispatch failure notification failed", "channel", c.Name, "err", derr)
		}
	}
}

// notifyRecovery 成功路径：取走 streak，计数曾达阈值才派发恢复通知（spec AC-4）。
func (s *Service) notifyRecovery(ctx context.Context, c *storage.Channel, job storage.MonitorJob) {
	if m := s.failures.OnSuccess(*c, job); m != nil {
		if derr := s.dispatcher.Dispatch(ctx, *m); derr != nil {
			s.log.Warn("dispatch recovery notification failed", "channel", c.Name, "err", derr)
		}
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
