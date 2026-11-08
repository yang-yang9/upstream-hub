package monitor

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/worryzyy/upstream-hub/internal/storage"
)

// TDD red：FailureTracker / shouldAlertFailure / recoveryEligible / jobLabel 尚不存在
// （将在 monitor/failure.go 实现）。对应 spec：docs/specs/failure-recovery.md。
//
// 约定：包内手写 fake（FailureStreakStore 接口缝隙），不用 mock 库。

// fakeStreakStore 是 FailureStreakStore 的内存 fake。
// RecordFailure 按 (channel, job) 计数递增；TakeStreak 取走预置 / 计数对应的
// streak 并删除（与真实现"读 + 删"语义一致）。
type fakeStreakStore struct {
	counts      map[string]int64
	streaks     map[string]storage.FailureStreak // 预置待取走的 streak
	recordErr   error
	takeErr     error
	recordCalls int
	takeCalls   int
}

func newFakeStreakStore() *fakeStreakStore {
	return &fakeStreakStore{
		counts:  map[string]int64{},
		streaks: map[string]storage.FailureStreak{},
	}
}

func streakKey(channelID uint, job storage.MonitorJob) string {
	return fmt.Sprintf("%d/%s", channelID, job)
}

func (f *fakeStreakStore) RecordFailure(channelID uint, job storage.MonitorJob) (int64, error) {
	f.recordCalls++
	if f.recordErr != nil {
		return 0, f.recordErr
	}
	k := streakKey(channelID, job)
	f.counts[k]++
	return f.counts[k], nil
}

func (f *fakeStreakStore) TakeStreak(channelID uint, job storage.MonitorJob) (*storage.FailureStreak, error) {
	f.takeCalls++
	if f.takeErr != nil {
		return nil, f.takeErr
	}
	k := streakKey(channelID, job)
	s, ok := f.streaks[k]
	if !ok {
		return nil, nil
	}
	delete(f.streaks, k)
	out := s
	return &out, nil
}

// ---------- 纯函数（AC-3 / AC-4 阈值判定） ----------

func TestShouldAlertFailure(t *testing.T) {
	for _, tc := range []struct {
		count     int64
		threshold int
		want      bool
	}{
		{1, 3, false},
		{2, 3, false},
		{3, 3, true},
		{7, 3, true},
		{1, 1, true}, // 阈值 <= 1：每次失败即告警
		{1, 0, true},
		{2, -1, true},
	} {
		if got := shouldAlertFailure(tc.count, tc.threshold); got != tc.want {
			t.Errorf("shouldAlertFailure(%d, %d) = %v, want %v", tc.count, tc.threshold, got, tc.want)
		}
	}
}

func TestRecoveryEligible(t *testing.T) {
	for _, tc := range []struct {
		count     int64
		threshold int
		want      bool
	}{
		{2, 3, false}, // 静默抖动：未达阈值不发恢复
		{3, 3, true},
		{5, 3, true},
		{1, 1, true},
	} {
		if got := recoveryEligible(tc.count, tc.threshold); got != tc.want {
			t.Errorf("recoveryEligible(%d, %d) = %v, want %v", tc.count, tc.threshold, got, tc.want)
		}
	}
}

// ---------- job 标签与事件映射（AC-5） ----------

func TestJobLabel(t *testing.T) {
	for _, tc := range []struct {
		job  storage.MonitorJob
		want string
	}{
		{storage.MonitorJobLogin, "登录"},
		{storage.MonitorJobBalance, "余额采集"},
		{storage.MonitorJobRates, "倍率采集"},
	} {
		if got := jobLabel(tc.job); got != tc.want {
			t.Errorf("jobLabel(%v) = %q, want %q", tc.job, got, tc.want)
		}
	}
}

// ---------- FailureTracker.OnFailure（AC-3） ----------

func TestFailureTrackerOnFailure(t *testing.T) {
	c := storage.Channel{ID: 7, Name: "alpha"}
	boom := errors.New("dial tcp: boom")

	t.Run("below threshold stays silent", func(t *testing.T) {
		f := newFakeStreakStore()
		tr := NewFailureTracker(f, 3, nil)
		if m := tr.OnFailure(c, storage.MonitorJobLogin, boom); m != nil {
			t.Errorf("failure #1 should be silent, got %+v", m)
		}
		if m := tr.OnFailure(c, storage.MonitorJobLogin, boom); m != nil {
			t.Errorf("failure #2 should be silent, got %+v", m)
		}
	})

	t.Run("reaching threshold alerts with count", func(t *testing.T) {
		f := newFakeStreakStore()
		tr := NewFailureTracker(f, 3, nil)
		tr.OnFailure(c, storage.MonitorJobLogin, boom)
		tr.OnFailure(c, storage.MonitorJobLogin, boom)
		m := tr.OnFailure(c, storage.MonitorJobLogin, boom)
		if m == nil {
			t.Fatal("failure #3 should alert")
		}
		if m.Event != storage.EventLoginFailed {
			t.Errorf("event = %v, want login_failed", m.Event)
		}
		if m.ChannelID != c.ID {
			t.Errorf("channel_id = %d, want %d", m.ChannelID, c.ID)
		}
		if want := "[upstream-hub] alpha 登录失败"; m.Subject != want {
			t.Errorf("subject = %q, want %q", m.Subject, want)
		}
		if !strings.Contains(m.Body, "连续失败 3 次") || !strings.Contains(m.Body, "dial tcp: boom") {
			t.Errorf("body %q should contain error text and streak count", m.Body)
		}
	})

	t.Run("subsequent failures keep alerting", func(t *testing.T) {
		// 达阈值后的持续失败继续返回告警；重发节流由 dispatcher 的失败冷却负责。
		f := newFakeStreakStore()
		tr := NewFailureTracker(f, 3, nil)
		for i := 0; i < 3; i++ {
			tr.OnFailure(c, storage.MonitorJobLogin, boom)
		}
		m := tr.OnFailure(c, storage.MonitorJobLogin, boom)
		if m == nil {
			t.Fatal("failure #4 should still alert")
		}
		if !strings.Contains(m.Body, "连续失败 4 次") {
			t.Errorf("body %q should contain updated streak count", m.Body)
		}
	})

	t.Run("balance job maps to monitor_failed", func(t *testing.T) {
		f := newFakeStreakStore()
		tr := NewFailureTracker(f, 3, nil)
		for i := 0; i < 3; i++ {
			tr.OnFailure(c, storage.MonitorJobBalance, boom)
		}
		// 第 3 次已把 counts 攒够：直接检查事件映射。
		m := tr.OnFailure(c, storage.MonitorJobBalance, boom)
		if m == nil || m.Event != storage.EventMonitorFailed {
			t.Fatalf("balance job alert = %+v, want monitor_failed event", m)
		}
		if want := "[upstream-hub] alpha 余额采集失败"; m.Subject != want {
			t.Errorf("subject = %q, want %q", m.Subject, want)
		}
	})

	t.Run("rates job maps to monitor_failed", func(t *testing.T) {
		f := newFakeStreakStore()
		tr := NewFailureTracker(f, 3, nil)
		for i := 0; i < 3; i++ {
			tr.OnFailure(c, storage.MonitorJobRates, boom)
		}
		m := tr.OnFailure(c, storage.MonitorJobRates, boom)
		if m == nil || m.Event != storage.EventMonitorFailed {
			t.Fatalf("rates job alert = %+v, want monitor_failed event", m)
		}
		if want := "[upstream-hub] alpha 倍率采集失败"; m.Subject != want {
			t.Errorf("subject = %q, want %q", m.Subject, want)
		}
	})

	t.Run("threshold <= 1 alerts on first failure", func(t *testing.T) {
		f := newFakeStreakStore()
		tr := NewFailureTracker(f, 1, nil)
		m := tr.OnFailure(c, storage.MonitorJobLogin, boom)
		if m == nil {
			t.Fatal("threshold=1 should alert on first failure")
		}
		if !strings.Contains(m.Body, "连续失败 1 次") {
			t.Errorf("body %q should contain streak count 1", m.Body)
		}
	})
}

// streak 存储出错时 fail-open：照发告警（与 dispatcher 冷却的 fail-open 同语义），
// 只是正文拿不到计数，不附"连续失败 N 次"。
func TestFailureTrackerOnFailureStoreError(t *testing.T) {
	c := storage.Channel{ID: 7, Name: "alpha"}
	boom := errors.New("dial tcp: boom")

	f := newFakeStreakStore()
	f.recordErr = errors.New("db down")
	tr := NewFailureTracker(f, 3, nil)
	m := tr.OnFailure(c, storage.MonitorJobLogin, boom)
	if m == nil {
		t.Fatal("store error should fail-open: still alert")
	}
	if m.Event != storage.EventLoginFailed {
		t.Errorf("event = %v, want login_failed", m.Event)
	}
	if strings.Contains(m.Body, "连续失败") {
		t.Errorf("count unknown on store error, body %q should not claim a count", m.Body)
	}
	if !strings.Contains(m.Body, "dial tcp: boom") {
		t.Errorf("body %q should contain error text", m.Body)
	}
}

// ---------- FailureTracker.OnSuccess（AC-4） ----------

func TestFailureTrackerOnSuccess(t *testing.T) {
	c := storage.Channel{ID: 7, Name: "alpha"}

	t.Run("healthy channel without streak", func(t *testing.T) {
		f := newFakeStreakStore()
		tr := NewFailureTracker(f, 3, nil)
		if m := tr.OnSuccess(c, storage.MonitorJobBalance); m != nil {
			t.Errorf("no streak should send nothing, got %+v", m)
		}
		if f.takeCalls != 1 {
			t.Errorf("takeCalls = %d, want 1", f.takeCalls)
		}
	})

	t.Run("below threshold streak cleared silently", func(t *testing.T) {
		f := newFakeStreakStore()
		f.streaks[streakKey(7, storage.MonitorJobBalance)] = storage.FailureStreak{Count: 2}
		tr := NewFailureTracker(f, 3, nil)
		if m := tr.OnSuccess(c, storage.MonitorJobBalance); m != nil {
			t.Errorf("below-threshold streak should be cleared silently, got %+v", m)
		}
		if _, ok := f.streaks[streakKey(7, storage.MonitorJobBalance)]; ok {
			t.Error("streak should be consumed on success")
		}
	})

	t.Run("threshold streak sends login recovery", func(t *testing.T) {
		f := newFakeStreakStore()
		f.streaks[streakKey(7, storage.MonitorJobLogin)] = storage.FailureStreak{Count: 3}
		tr := NewFailureTracker(f, 3, nil)
		m := tr.OnSuccess(c, storage.MonitorJobLogin)
		if m == nil {
			t.Fatal("streak reached threshold: recovery should be sent")
		}
		if m.Event != storage.EventLoginRecovered {
			t.Errorf("event = %v, want login_recovered", m.Event)
		}
		if m.ChannelID != c.ID {
			t.Errorf("channel_id = %d, want %d", m.ChannelID, c.ID)
		}
		if want := "[upstream-hub] alpha 已恢复（登录）"; m.Subject != want {
			t.Errorf("subject = %q, want %q", m.Subject, want)
		}
		if !strings.Contains(m.Body, "此前连续失败 3 次") {
			t.Errorf("body %q should contain previous streak count", m.Body)
		}
	})

	t.Run("balance job maps to monitor_recovered", func(t *testing.T) {
		f := newFakeStreakStore()
		f.streaks[streakKey(7, storage.MonitorJobBalance)] = storage.FailureStreak{Count: 4}
		tr := NewFailureTracker(f, 3, nil)
		m := tr.OnSuccess(c, storage.MonitorJobBalance)
		if m == nil || m.Event != storage.EventMonitorRecovered {
			t.Fatalf("balance recovery = %+v, want monitor_recovered event", m)
		}
		if want := "[upstream-hub] alpha 已恢复（余额采集）"; m.Subject != want {
			t.Errorf("subject = %q, want %q", m.Subject, want)
		}
	})

	t.Run("rates job maps to monitor_recovered", func(t *testing.T) {
		f := newFakeStreakStore()
		f.streaks[streakKey(7, storage.MonitorJobRates)] = storage.FailureStreak{Count: 4}
		tr := NewFailureTracker(f, 3, nil)
		m := tr.OnSuccess(c, storage.MonitorJobRates)
		if m == nil || m.Event != storage.EventMonitorRecovered {
			t.Fatalf("rates recovery = %+v, want monitor_recovered event", m)
		}
		if want := "[upstream-hub] alpha 已恢复（倍率采集）"; m.Subject != want {
			t.Errorf("subject = %q, want %q", m.Subject, want)
		}
	})

	t.Run("take error stays silent", func(t *testing.T) {
		f := newFakeStreakStore()
		f.takeErr = errors.New("db down")
		tr := NewFailureTracker(f, 3, nil)
		if m := tr.OnSuccess(c, storage.MonitorJobLogin); m != nil {
			t.Errorf("take error should stay silent, got %+v", m)
		}
	})

	t.Run("threshold 1 recovers after single failure", func(t *testing.T) {
		f := newFakeStreakStore()
		f.streaks[streakKey(7, storage.MonitorJobLogin)] = storage.FailureStreak{Count: 1}
		tr := NewFailureTracker(f, 1, nil)
		m := tr.OnSuccess(c, storage.MonitorJobLogin)
		if m == nil {
			t.Fatal("threshold=1: single failure then success should send recovery")
		}
		if !strings.Contains(m.Body, "此前连续失败 1 次") {
			t.Errorf("body %q should contain streak count 1", m.Body)
		}
	})
}
