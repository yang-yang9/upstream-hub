// Package scheduler 用 robfig/cron 触发周期性扫描。
package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/worryzyy/upstream-hub/internal/config"
	"github.com/worryzyy/upstream-hub/internal/monitor"
	"github.com/worryzyy/upstream-hub/internal/storage"
)

type Scheduler struct {
	cfg      config.SchedulerConfig
	log      *slog.Logger
	cron     *cron.Cron
	monitor  *monitor.Service
	monLogs  *storage.MonitorLogs
	rates    *storage.Rates
	notifies *storage.Notifications
}

func New(
	cfg config.SchedulerConfig,
	m *monitor.Service,
	monLogs *storage.MonitorLogs,
	rates *storage.Rates,
	notifies *storage.Notifications,
	log *slog.Logger,
) *Scheduler {
	return &Scheduler{
		cfg:      cfg,
		log:      log,
		cron:     cron.New(cron.WithSeconds()),
		monitor:  m,
		monLogs:  monLogs,
		rates:    rates,
		notifies: notifies,
	}
}

func (s *Scheduler) Start() error {
	if s.cfg.BalanceCron != "" {
		if _, err := s.cron.AddFunc(s.cfg.BalanceCron, s.runBalance); err != nil {
			return err
		}
	}
	if s.cfg.RateCron != "" {
		if _, err := s.cron.AddFunc(s.cfg.RateCron, s.runRates); err != nil {
			return err
		}
	}
	if s.cfg.Retention.Cron != "" && s.hasRetention() {
		if _, err := s.cron.AddFunc(s.cfg.Retention.Cron, s.runRetention); err != nil {
			return err
		}
	}
	s.cron.Start()
	s.log.Info("scheduler started",
		"balanceCron", s.cfg.BalanceCron,
		"rateCron", s.cfg.RateCron,
		"retentionCron", s.cfg.Retention.Cron,
		"concurrency", s.cfg.Concurrency,
	)
	return nil
}

func (s *Scheduler) Stop() {
	if s.cron != nil {
		<-s.cron.Stop().Done()
	}
}

func (s *Scheduler) runBalance() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	s.monitor.ScanAllBalances(ctx)
}

func (s *Scheduler) runRates() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	s.monitor.ScanAllRates(ctx)
}

func (s *Scheduler) hasRetention() bool {
	r := s.cfg.Retention
	return r.MonitorLogsDays > 0 || r.BalanceSnapshotsDays > 0 || r.NotificationLogsDays > 0
}

// runRetention 按配置删除过期历史。任一表失败不影响其它，全部错误写日志。
func (s *Scheduler) runRetention() {
	for _, task := range planRetention(s.cfg.Retention, time.Now()) {
		s.runRetentionTask(task)
	}
}

// runRetentionTask 执行单张表的清理任务，失败只写日志。
func (s *Scheduler) runRetentionTask(task retentionTask) {
	var n int64
	var err error
	switch task.kind {
	case retentionMonitorLogs:
		n, err = s.monLogs.DeleteBefore(task.cutoff)
	case retentionBalanceSnapshots:
		n, err = s.rates.DeleteBalanceSnapshotsBefore(task.cutoff)
	default:
		n, err = s.notifies.DeleteLogsBefore(task.cutoff)
	}
	table := task.kind.table()
	if err != nil {
		s.log.Warn("retention "+table+" failed", "err", err)
	} else if n > 0 {
		s.log.Info("retention "+table+" deleted", "rows", n, "before", task.cutoff)
	}
}
