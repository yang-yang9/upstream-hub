// failure.go 失败风暴防刷屏与恢复通知的决策逻辑，脱离 DB 单测（fake store 缝隙）。
// 对应 spec：docs/specs/failure-recovery.md。
package monitor

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/worryzyy/upstream-hub/internal/notify"
	"github.com/worryzyy/upstream-hub/internal/storage"
)

// FailureStreakStore 连续失败计数的持久化窄接口。
// *storage.Failures 天然满足；测试用 fake 实现替代真 PostgreSQL（同 notify.CooldownStore 模式）。
type FailureStreakStore interface {
	// RecordFailure 原子递增并返回递增后的连续失败次数。
	RecordFailure(channelID uint, job storage.MonitorJob) (int64, error)
	// TakeStreak 读取并删除 streak；不存在返回 (nil, nil)。
	TakeStreak(channelID uint, job storage.MonitorJob) (*storage.FailureStreak, error)
}

var _ FailureStreakStore = (*storage.Failures)(nil)

// FailureTracker 把"连续 N 次失败才告警"与"成功后恢复通知"集中在 monitor 层。
//
// 计数按 job 维度（login / balance / rates）而非事件维度：monitor_failed 事件被
// balance 与 rates 两个 job 共用，按事件计数会被对方的成功稀释（spec 背景）。
// 派发出去的消息由 Dispatcher 的失败冷却做重发节流，tracker 不做"已告警"标记。
type FailureTracker struct {
	store FailureStreakStore
	log   *slog.Logger

	// mu 保护 threshold：UpdateThreshold（P3-14 热更新）可与扫描中的
	// OnFailure / OnSuccess 并发。
	mu        sync.RWMutex
	threshold int
}

func NewFailureTracker(store FailureStreakStore, threshold int, log *slog.Logger) *FailureTracker {
	return &FailureTracker{store: store, threshold: threshold, log: log}
}

// UpdateThreshold 热更新连续失败阈值（P3-14，docs/specs/runtime-settings.md
// AC-4）：缩小后已达新阈值的 streak 在下次失败时立即告警；放大后未达
// 新阈值的 streak 保持静默。既有 streak 计数不动。
func (t *FailureTracker) UpdateThreshold(n int) {
	t.mu.Lock()
	t.threshold = n
	t.mu.Unlock()
}

// thresholdSnapshot 当前阈值快照（与 UpdateThreshold 并发安全）。
func (t *FailureTracker) thresholdSnapshot() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.threshold
}

// OnFailure 记录一次连续失败，返回应派发的失败告警（nil = 静默）。
// streak 存储出错时 fail-open：照发告警但不附计数（与 dispatcher 冷却的 fail-open 同语义，
// 漏计数可接受，漏告警不可接受）。
func (t *FailureTracker) OnFailure(c storage.Channel, job storage.MonitorJob, err error) *notify.Message {
	count, sErr := t.store.RecordFailure(c.ID, job)
	if sErr != nil {
		t.warn("record failure streak failed", c, job, sErr)
		return failureMessage(c, job, err, 0)
	}
	if !shouldAlertFailure(count, t.thresholdSnapshot()) {
		return nil
	}
	return failureMessage(c, job, err, count)
}

// OnSuccess 在 job 成功后取走 streak，返回应派发的恢复通知（nil = 静默）。
// 未达阈值的抖动静默清除；取走出错时静默返回——streak 留在库里等下次成功再取，
// 宁可晚发恢复也不误发"已恢复"。
func (t *FailureTracker) OnSuccess(c storage.Channel, job storage.MonitorJob) *notify.Message {
	s, err := t.store.TakeStreak(c.ID, job)
	if err != nil {
		t.warn("take failure streak failed", c, job, err)
		return nil
	}
	if s == nil || !recoveryEligible(s.Count, t.thresholdSnapshot()) {
		return nil
	}
	return &notify.Message{
		Event:     recoveryEventFor(job),
		ChannelID: c.ID,
		Subject:   fmt.Sprintf("[upstream-hub] %s 已恢复（%s）", c.Name, jobLabel(job)),
		Body:      fmt.Sprintf("此前连续失败 %d 次", s.Count),
		// 恢复事件不参与冷却，但照常携带覆盖（Dispatcher 对非冷却事件忽略它）。
		Cooldown: cooldownOverrides(&c),
	}
}

// failureMessage 失败告警。count = 0 表示 streak 存储出错、计数未知，正文不附次数。
// 标题格式与既有 notifyError 保持一致，正文追加"连续失败 N 次"（spec AC-3）。
func failureMessage(c storage.Channel, job storage.MonitorJob, err error, count int64) *notify.Message {
	body := err.Error()
	if count > 0 {
		body = fmt.Sprintf("%s\n连续失败 %d 次", body, count)
	}
	return &notify.Message{
		Event:     failureEventFor(job),
		ChannelID: c.ID,
		Subject:   fmt.Sprintf("[upstream-hub] %s %s失败", c.Name, jobLabel(job)),
		Body:      body,
		Cooldown:  cooldownOverrides(&c),
	}
}

func (t *FailureTracker) warn(msg string, c storage.Channel, job storage.MonitorJob, err error) {
	if t.log != nil {
		t.log.Warn(msg, "channel", c.Name, "job", string(job), "err", err)
	}
}

// shouldAlertFailure 连续失败达到阈值才告警；threshold <= 1 时每次失败即告警。
func shouldAlertFailure(count int64, threshold int) bool {
	return count >= int64(threshold)
}

// recoveryEligible streak 计数曾达阈值（即派发过失败告警）才发恢复通知；
// 静默抖动（未达阈值即恢复）不打扰用户。
func recoveryEligible(count int64, threshold int) bool {
	return count >= int64(threshold)
}

// jobLabel job 在通知文案中的中文标签。
func jobLabel(job storage.MonitorJob) string {
	switch job {
	case storage.MonitorJobLogin:
		return "登录"
	case storage.MonitorJobBalance:
		return "余额采集"
	case storage.MonitorJobRates:
		return "倍率采集"
	}
	return string(job)
}

// failureEventFor / recoveryEventFor job → 通知事件映射（spec AC-5）：
// login 独占 login_failed / login_recovered；balance 与 rates 共用
// monitor_failed / monitor_recovered——冷却按 (channel, event) 共享，
// 属可接受的粗粒度（都是"该渠道采集异常"信号）。
func failureEventFor(job storage.MonitorJob) storage.NotificationEvent {
	if job == storage.MonitorJobLogin {
		return storage.EventLoginFailed
	}
	return storage.EventMonitorFailed
}

func recoveryEventFor(job storage.MonitorJob) storage.NotificationEvent {
	if job == storage.MonitorJobLogin {
		return storage.EventLoginRecovered
	}
	return storage.EventMonitorRecovered
}
