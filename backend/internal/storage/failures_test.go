// storage 集成测试（外部包 storage_test）：连续失败 streak。
// env 门控 UPSTREAMHUB_TEST_DB=1（见 docs/development.md 第三节）。
// 对应 spec：docs/specs/failure-recovery.md AC-2 / AC-4。
package storage_test

import (
	"sync"
	"testing"
	"time"

	"github.com/worryzyy/upstream-hub/internal/storage"
	"github.com/worryzyy/upstream-hub/internal/storage/storagetest"
)

func TestFailureStreakIncrementAndTake(t *testing.T) {
	db := storagetest.Open(t)
	f := storage.NewFailures(db)

	count, err := f.RecordFailure(1, storage.MonitorJobLogin)
	if err != nil {
		t.Fatalf("RecordFailure #1: %v", err)
	}
	if count != 1 {
		t.Fatalf("first count = %d, want 1", count)
	}
	count, err = f.RecordFailure(1, storage.MonitorJobLogin)
	if err != nil {
		t.Fatalf("RecordFailure #2: %v", err)
	}
	if count != 2 {
		t.Fatalf("second count = %d, want 2", count)
	}

	s, err := f.TakeStreak(1, storage.MonitorJobLogin)
	if err != nil {
		t.Fatalf("TakeStreak: %v", err)
	}
	if s == nil {
		t.Fatal("TakeStreak should return the recorded streak")
	}
	if s.Count != 2 || s.ChannelID != 1 || s.Job != storage.MonitorJobLogin {
		t.Errorf("streak = %+v, want count=2 channel=1 job=login", s)
	}

	// 取走即清除：再次 Take 返回 nil。
	again, err := f.TakeStreak(1, storage.MonitorJobLogin)
	if err != nil {
		t.Fatalf("TakeStreak again: %v", err)
	}
	if again != nil {
		t.Errorf("streak should be deleted after take, got %+v", again)
	}
}

// job 维度独立计数：balance 的成功不会清掉 login / rates 攒的失败计数。
func TestFailureStreakPerJob(t *testing.T) {
	db := storagetest.Open(t)
	f := storage.NewFailures(db)

	for i := 0; i < 2; i++ {
		if _, err := f.RecordFailure(1, storage.MonitorJobLogin); err != nil {
			t.Fatalf("RecordFailure login: %v", err)
		}
	}
	if _, err := f.RecordFailure(1, storage.MonitorJobBalance); err != nil {
		t.Fatalf("RecordFailure balance: %v", err)
	}

	s, err := f.TakeStreak(1, storage.MonitorJobLogin)
	if err != nil || s == nil {
		t.Fatalf("TakeStreak login = (%v, %v), want streak", s, err)
	}
	if s.Count != 2 {
		t.Errorf("login count = %d, want 2", s.Count)
	}
	s, err = f.TakeStreak(1, storage.MonitorJobBalance)
	if err != nil || s == nil {
		t.Fatalf("TakeStreak balance = (%v, %v), want streak", s, err)
	}
	if s.Count != 1 {
		t.Errorf("balance count = %d, want 1", s.Count)
	}
}

// first_failed_at 首写后不变，last_failed_at 随递增刷新（AC-2）。
func TestFailureStreakTimestamps(t *testing.T) {
	db := storagetest.Open(t)
	f := storage.NewFailures(db)

	if _, err := f.RecordFailure(1, storage.MonitorJobRates); err != nil {
		t.Fatalf("RecordFailure #1: %v", err)
	}
	var first storage.FailureStreak
	if err := db.Where("channel_id = ? AND job = ?", 1, storage.MonitorJobRates).
		First(&first).Error; err != nil {
		t.Fatalf("load streak: %v", err)
	}

	time.Sleep(20 * time.Millisecond)
	if _, err := f.RecordFailure(1, storage.MonitorJobRates); err != nil {
		t.Fatalf("RecordFailure #2: %v", err)
	}
	var second storage.FailureStreak
	if err := db.Where("channel_id = ? AND job = ?", 1, storage.MonitorJobRates).
		First(&second).Error; err != nil {
		t.Fatalf("reload streak: %v", err)
	}

	if !second.FirstFailedAt.Equal(first.FirstFailedAt) {
		t.Errorf("first_failed_at changed: %v -> %v", first.FirstFailedAt, second.FirstFailedAt)
	}
	if !second.LastFailedAt.After(first.LastFailedAt) {
		t.Errorf("last_failed_at should refresh: %v -> %v", first.LastFailedAt, second.LastFailedAt)
	}
}

// 并发递增原子：N 个 goroutine 各递增 M 次，最终计数恰好 N*M（AC-2）。
func TestFailureStreakConcurrentIncrement(t *testing.T) {
	db := storagetest.Open(t)
	f := storage.NewFailures(db)

	const goroutines = 20
	const per = 5
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < per; j++ {
				if _, err := f.RecordFailure(9, storage.MonitorJobBalance); err != nil {
					t.Errorf("RecordFailure: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	s, err := f.TakeStreak(9, storage.MonitorJobBalance)
	if err != nil {
		t.Fatalf("TakeStreak: %v", err)
	}
	if s == nil {
		t.Fatal("TakeStreak should return the streak")
	}
	if want := int64(goroutines * per); s.Count != want {
		t.Fatalf("concurrent count = %d, want %d", s.Count, want)
	}
}
