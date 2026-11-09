// Package scheduler 用 robfig/cron 触发周期性扫描。
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/worryzyy/upstream-hub/internal/config"
	"github.com/worryzyy/upstream-hub/internal/monitor"
	"github.com/worryzyy/upstream-hub/internal/storage"
)

// cronParser 与 cron.New(cron.WithSeconds()) 的解析能力一致：
// 6 段（秒 分 时 日 月 周）+ @descriptor。ParseCron 导出给
// settings.Validate 复用，保证校验与实际调度用同一套语法。
var cronParser = cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// ParseCron 解析 cron 表达式（6 段含秒，或 @every/@daily 等 descriptor）。
// 空串报错；"是否为空 = 不调度"由调用方单独判断。
func ParseCron(expr string) (cron.Schedule, error) {
	return cronParser.Parse(expr)
}

type Scheduler struct {
	cfg      config.SchedulerConfig
	log      *slog.Logger
	cron     *cron.Cron
	monitor  *monitor.Service
	monLogs  *storage.MonitorLogs
	rates    *storage.Rates
	notifies *storage.Notifications

	mu      sync.Mutex // 保护 cfg 与 entries（Reconfigure 可与运行中的 cron 并发）
	entries map[string]cron.EntryID
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
		entries:  map[string]cron.EntryID{},
	}
}

func (s *Scheduler) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.entries) == 0 {
		// Reconfigure 已注册过（Start 前 Reconfigure = 先改配置再启动），
		// 此时不再重复注册。
		if err := s.registerEntries(); err != nil {
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

// registerEntries 按当前 cfg 注册全部任务（调用方须持 mu）。先整体解析
// 再注册，避免半注册状态；表达式已解析过，AddFunc 不会失败。
func (s *Scheduler) registerEntries() error {
	type job struct {
		name string
		expr string
		fn   func()
	}
	jobs := []job{
		{"balance", s.cfg.BalanceCron, s.runBalance},
		{"rate", s.cfg.RateCron, s.runRates},
	}
	if s.cfg.Retention.Cron != "" && s.hasRetention() {
		jobs = append(jobs, job{"retention", s.cfg.Retention.Cron, s.runRetention})
	}
	// 空 = 不调度该任务（合法；热更新允许关掉某个扫描）。
	n := 0
	for _, j := range jobs {
		if j.expr != "" {
			jobs[n] = j
			n++
		}
	}
	jobs = jobs[:n]
	for _, j := range jobs {
		id, err := s.cron.AddFunc(j.expr, j.fn)
		if err != nil {
			return fmt.Errorf("add %s job %q: %w", j.name, j.expr, err)
		}
		s.entries[j.name] = id
	}
	return nil
}

// clearEntries 撤销全部已注册任务（调用方须持 mu）。
func (s *Scheduler) clearEntries() {
	for _, id := range s.entries {
		s.cron.Remove(id)
	}
	s.entries = map[string]cron.EntryID{}
}

// Reconfigure 整体替换调度配置（P3-14 热更新）。先校验全部非空表达式，
// 任一非法即拒绝且现场保持不变；合法则换 cfg、重注册 entry（cron 已在
// 运行时新 entry 立即生效，被移除的旧 entry 不再触发）。Start 前调用等价于
// 先改配置再启动。
func (s *Scheduler) Reconfigure(cfg config.SchedulerConfig) error {
	for _, expr := range []string{cfg.BalanceCron, cfg.RateCron, cfg.Retention.Cron} {
		if expr == "" {
			continue
		}
		if _, err := ParseCron(expr); err != nil {
			return fmt.Errorf("invalid cron %q: %w", expr, err)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = cfg
	s.clearEntries()
	if err := s.registerEntries(); err != nil {
		return err
	}
	s.log.Info("scheduler reconfigured",
		"balanceCron", s.cfg.BalanceCron,
		"rateCron", s.cfg.RateCron,
		"retentionCron", s.cfg.Retention.Cron,
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
