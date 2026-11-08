// storage 集成测试（外部包 storage_test）：监控日志 + 渠道仓储小用例。
package storage_test

import (
	"testing"
	"time"

	"github.com/worryzyy/upstream-hub/internal/storage"
	"github.com/worryzyy/upstream-hub/internal/storage/storagetest"
)

func TestMonitorLogsAppendDefaults(t *testing.T) {
	m := storagetest.MonitorLogs(t)

	// 零值自动填：StartedAt / FinishedAt / DurationMS。
	l := &storage.MonitorLog{ChannelID: 1, Job: storage.MonitorJobBalance, Success: true}
	if err := m.Append(l); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if l.StartedAt.IsZero() || l.FinishedAt.IsZero() || l.DurationMS != 0 {
		t.Errorf("defaults not filled: %+v", l)
	}

	// 显式时间与耗时保留。
	start := time.Now().UTC().Truncate(time.Microsecond)
	finish := start.Add(150 * time.Millisecond)
	if err := m.Append(&storage.MonitorLog{
		ChannelID: 2, Job: storage.MonitorJobRates, Success: false,
		ErrorMessage: "boom", StartedAt: start, FinishedAt: finish,
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	logs, err := m.List(0, 0)
	if err != nil || len(logs) != 2 {
		t.Fatalf("List = %v, %v", logs, err)
	}
	// 倒序：自动填时间的新日志在前。
	if logs[0].ChannelID != 1 || logs[1].ChannelID != 2 {
		t.Errorf("List order = %v then %v, want newest first", logs[0].ChannelID, logs[1].ChannelID)
	}
	if !logs[1].StartedAt.Equal(start) || logs[1].DurationMS != 150 {
		t.Errorf("explicit log = %+v, want start preserved and 150ms", logs[1])
	}

	// channelID 过滤。
	only1, err := m.List(1, 0)
	if err != nil || len(only1) != 1 || only1[0].ChannelID != 1 {
		t.Errorf("List(1) = %v, %v", only1, err)
	}
}

func TestMonitorLogsDeleteBefore(t *testing.T) {
	m := storagetest.MonitorLogs(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	cutoff := now.Add(-24 * time.Hour)

	for _, at := range []time.Time{now.Add(-48 * time.Hour), cutoff, now.Add(-1 * time.Hour)} {
		if err := m.Append(&storage.MonitorLog{
			ChannelID: 1, Job: storage.MonitorJobBalance, Success: true,
			StartedAt: at, FinishedAt: at,
		}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	// 严格小于：等于 cutoff 的保留。
	n, err := m.DeleteBefore(cutoff)
	if err != nil {
		t.Fatalf("DeleteBefore: %v", err)
	}
	if n != 1 {
		t.Errorf("deleted = %d, want 1 (strict less-than)", n)
	}
	logs, _ := m.List(0, 0)
	if len(logs) != 2 {
		t.Errorf("remaining logs = %d, want 2", len(logs))
	}
}

func TestChannelsMonitorAndBalance(t *testing.T) {
	r := storagetest.Channels(t)
	now := time.Now().UTC().Truncate(time.Microsecond)

	for _, c := range []*storage.Channel{
		{Name: "on", Type: storage.ChannelTypeNewAPI, SiteURL: "https://a.example", Username: "u", MonitorEnabled: true},
		{Name: "off", Type: storage.ChannelTypeNewAPI, SiteURL: "https://b.example", Username: "u", MonitorEnabled: false},
	} {
		if err := r.Create(c); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	list, err := r.ListMonitorEnabled()
	if err != nil || len(list) != 1 || list[0].Name != "on" {
		t.Fatalf("ListMonitorEnabled = %v, %v", list, err)
	}

	// UpdateBalance 聚合字段写回。
	bal := 12.5
	if err := r.UpdateBalance(list[0].ID, bal, now, ""); err != nil {
		t.Fatalf("UpdateBalance: %v", err)
	}
	got, err := r.FindByID(list[0].ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.LastBalance == nil || *got.LastBalance != bal {
		t.Errorf("LastBalance = %v, want %v", got.LastBalance, bal)
	}
	if got.LastBalanceAt == nil || !got.LastBalanceAt.Equal(now) {
		t.Errorf("LastBalanceAt = %v, want %v", got.LastBalanceAt, now)
	}
	if got.LastError != "" {
		t.Errorf("LastError = %q, want empty", got.LastError)
	}

	// SetLastError 独立更新错误字段。
	if err := r.SetLastError(list[0].ID, "boom"); err != nil {
		t.Fatalf("SetLastError: %v", err)
	}
	got, _ = r.FindByID(list[0].ID)
	if got.LastError != "boom" {
		t.Errorf("LastError = %q, want boom", got.LastError)
	}
}
