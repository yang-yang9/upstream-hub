// storage 集成测试（外部包 storage_test）：通知渠道 / 日志 / 冷却。
package storage_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/worryzyy/upstream-hub/internal/storage"
	"github.com/worryzyy/upstream-hub/internal/storage/storagetest"
)

func notifChannel(name string, enabled bool) *storage.NotificationChannel {
	return &storage.NotificationChannel{
		Name: name, Type: storage.NotifyWebhook,
		ConfigCipher: "cipher", Subscriptions: "[]", Enabled: enabled,
	}
}

func TestNotificationsListEnabledChannels(t *testing.T) {
	n := storagetest.Notifications(t)
	for _, c := range []*storage.NotificationChannel{
		notifChannel("on", true), notifChannel("off", false), notifChannel("on2", true),
	} {
		if err := n.CreateChannel(c); err != nil {
			t.Fatalf("CreateChannel: %v", err)
		}
	}
	got, err := n.ListEnabledChannels()
	if err != nil {
		t.Fatalf("ListEnabledChannels: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("enabled channels = %d, want 2", len(got))
	}
	for _, c := range got {
		if !c.Enabled {
			t.Errorf("disabled channel %q returned", c.Name)
		}
	}
}

func TestNotificationsLogs(t *testing.T) {
	n := storagetest.Notifications(t)
	now := time.Now().UTC().Truncate(time.Microsecond)

	// SentAt 为零时自动填。
	if err := n.AppendLog(&storage.NotificationLog{
		ChannelID: 1, Event: storage.EventBalanceLow, Subject: "s1", Body: "b1", Success: true,
	}); err != nil {
		t.Fatalf("AppendLog: %v", err)
	}
	if err := n.AppendLog(&storage.NotificationLog{
		ChannelID: 1, Event: storage.EventRateChanged, Subject: "s2", Body: "b2",
		Success: false, ErrorMessage: "boom", SentAt: now.Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("AppendLog: %v", err)
	}

	logs, err := n.ListLogs(0)
	if err != nil || len(logs) != 2 {
		t.Fatalf("ListLogs = %v, %v", logs, err)
	}
	// 倒序：自动填时间的新日志在前。
	if logs[0].Subject != "s1" || logs[0].SentAt.Before(now.Add(-time.Minute)) {
		t.Errorf("newest log = %+v, want auto-filled sent_at", logs[0])
	}
	if logs[1].Subject != "s2" || !logs[1].SentAt.Equal(now.Add(-2*time.Hour)) {
		t.Errorf("older log = %+v, want explicit sent_at", logs[1])
	}

	// DeleteLogsBefore 严格小于 cutoff：正好等于 cutoff 的保留。
	n1, err := n.DeleteLogsBefore(now.Add(-2 * time.Hour))
	if err != nil || n1 != 0 {
		t.Errorf("delete at cutoff = %d, %v, want 0 (strict less-than)", n1, err)
	}
	n2, err := n.DeleteLogsBefore(now.Add(-2*time.Hour).Add(time.Microsecond))
	if err != nil || n2 != 1 {
		t.Errorf("delete just after cutoff = %d, %v, want 1", n2, err)
	}
}

func TestTryClaimCooldown(t *testing.T) {
	n := storagetest.Notifications(t)
	const cd = time.Hour

	// cooldown <= 0 短路：恒真且不写表。
	ok, err := n.TryClaimCooldown(1, storage.EventBalanceLow, 0)
	if err != nil || !ok {
		t.Fatalf("zero cooldown short-circuit = %v, %v, want true", ok, err)
	}

	// 首次 claim：插入记录，返回 true。
	ok, err = n.TryClaimCooldown(1, storage.EventBalanceLow, cd)
	if err != nil || !ok {
		t.Fatalf("first claim = %v, %v, want true", ok, err)
	}
	// 紧接着：仍在冷却窗口，返回 false。
	ok, err = n.TryClaimCooldown(1, storage.EventBalanceLow, cd)
	if err != nil || ok {
		t.Fatalf("second claim = %v, %v, want false", ok, err)
	}
	// 不同事件互不影响。
	ok, err = n.TryClaimCooldown(1, storage.EventRateChanged, cd)
	if err != nil || !ok {
		t.Fatalf("other event claim = %v, %v, want true", ok, err)
	}

	// 冷却已过（直接回拨 last_sent_at 模拟时间流逝）：再次 true。
	if err := n.ResetCooldown(1, storage.EventBalanceLow); err != nil {
		t.Fatalf("ResetCooldown: %v", err)
	}
	ok, err = n.TryClaimCooldown(1, storage.EventBalanceLow, cd)
	if err != nil || !ok {
		t.Errorf("claim after reset = %v, %v, want true", ok, err)
	}
}

func TestTryClaimCooldownConcurrentSingleWinner(t *testing.T) {
	n := storagetest.Notifications(t)
	const workers = 8

	var winners int64
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := n.TryClaimCooldown(42, storage.EventBalanceLow, time.Hour)
			if err != nil {
				t.Errorf("TryClaimCooldown: %v", err)
				return
			}
			if ok {
				atomic.AddInt64(&winners, 1)
			}
		}()
	}
	wg.Wait()
	if w := atomic.LoadInt64(&winners); w != 1 {
		t.Errorf("concurrent winners = %d, want exactly 1", w)
	}
}
