// storage 集成测试（外部包 storage_test）：余额恢复状态列（P0-3）。
// 对应 spec：docs/specs/balance-recovery.md AC-6。
package storage_test

import (
	"testing"

	"github.com/worryzyy/upstream-hub/internal/storage"
	"github.com/worryzyy/upstream-hub/internal/storage/storagetest"
)

func TestChannelBalanceLowActive(t *testing.T) {
	r := storagetest.Channels(t)

	c := &storage.Channel{
		Name:             "alpha",
		Type:             storage.ChannelTypeNewAPI,
		SiteURL:          "https://example.com",
		Username:         "u",
		CredentialMode:   storage.CredentialModePassword,
		MonitorEnabled:   true,
		BalanceThreshold: 10,
	}
	if err := r.Create(c); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.BalanceLowActive {
		t.Error("新建渠道 BalanceLowActive = true, want false（默认未处于低告警状态）")
	}

	// 标记低余额。
	if err := r.SetBalanceLowActive(c.ID, true); err != nil {
		t.Fatalf("SetBalanceLowActive(true): %v", err)
	}
	got, err := r.FindByID(c.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if !got.BalanceLowActive {
		t.Error("SetBalanceLowActive(true) 后 FindByID = false, want true")
	}

	// 扫描列表同样带出该字段（monitor 从 ListMonitorEnabled 读初始状态）。
	list, err := r.ListMonitorEnabled()
	if err != nil {
		t.Fatalf("ListMonitorEnabled: %v", err)
	}
	found := false
	for i := range list {
		if list[i].ID == c.ID {
			found = true
			if !list[i].BalanceLowActive {
				t.Error("ListMonitorEnabled 未带出 balance_low_active = true")
			}
		}
	}
	if !found {
		t.Fatal("ListMonitorEnabled 未包含新建渠道")
	}

	// 回升后清除。
	if err := r.SetBalanceLowActive(c.ID, false); err != nil {
		t.Fatalf("SetBalanceLowActive(false): %v", err)
	}
	got, err = r.FindByID(c.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.BalanceLowActive {
		t.Error("SetBalanceLowActive(false) 后 FindByID = true, want false")
	}
}
