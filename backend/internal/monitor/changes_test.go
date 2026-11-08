package monitor

import (
	"testing"
	"time"

	"github.com/worryzyy/upstream-hub/internal/notify"
	"github.com/worryzyy/upstream-hub/internal/storage"
)

// TDD red：rateChangeDiff / balanceLowMessage 尚不存在（将在 monitor/changes.go 抽出）。
// 测试精确编码 service.go 现有内联逻辑，抽取后行为不变。
// 对应 spec：docs/specs/monitor-rate-scanning.md。

func TestRateChangeDiff(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	c := &storage.Channel{ID: 7}

	for _, tc := range []struct {
		name      string
		prev      *storage.RateSnapshot
		ratio     float64
		comp      float64
		wantDelta bool
	}{
		{"first seen not a change", nil, 2, 3, false},
		{"both unchanged", &storage.RateSnapshot{Ratio: 2, CompletionRatio: 3}, 2, 3, false},
		{"ratio changed only", &storage.RateSnapshot{Ratio: 2, CompletionRatio: 3}, 2.5, 3, true},
		{"completion changed only", &storage.RateSnapshot{Ratio: 2, CompletionRatio: 3}, 2, 4, true},
		{"both changed", &storage.RateSnapshot{Ratio: 2, CompletionRatio: 3}, 1, 5, true},
	} {
		log, ch, changed := rateChangeDiff(c, "g1", tc.prev, tc.ratio, tc.comp, now)
		if changed != tc.wantDelta {
			t.Errorf("%s: changed = %v, want %v", tc.name, changed, tc.wantDelta)
			continue
		}
		if !changed {
			continue
		}

		// RateChangeLog 字段与指针语义。
		if log == nil {
			t.Fatalf("%s: log is nil", tc.name)
		}
		if log.ChannelID != 7 || log.ModelName != "g1" {
			t.Errorf("%s: log channel/model = %d/%q", tc.name, log.ChannelID, log.ModelName)
		}
		if log.OldRatio == nil || *log.OldRatio != tc.prev.Ratio {
			t.Errorf("%s: *log.OldRatio = %v, want %v", tc.name, log.OldRatio, tc.prev.Ratio)
		}
		if log.OldCompletionRatio == nil || *log.OldCompletionRatio != tc.prev.CompletionRatio {
			t.Errorf("%s: *log.OldCompletionRatio = %v, want %v", tc.name, log.OldCompletionRatio, tc.prev.CompletionRatio)
		}
		if log.NewRatio != tc.ratio || log.NewCompletionRatio != tc.comp {
			t.Errorf("%s: log new = %v/%v, want %v/%v", tc.name, log.NewRatio, log.NewCompletionRatio, tc.ratio, tc.comp)
		}
		if !log.ChangedAt.Equal(now) {
			t.Errorf("%s: ChangedAt = %v, want %v", tc.name, log.ChangedAt, now)
		}

		// notify.RateChange 投影。
		if ch.GroupName != "g1" || ch.OldRatio != tc.prev.Ratio || ch.NewRatio != tc.ratio ||
			ch.OldComp != tc.prev.CompletionRatio || ch.NewComp != tc.comp || !ch.ChangedAt.Equal(now) {
			t.Errorf("%s: RateChange = %+v", tc.name, ch)
		}
	}

	// 未变化时返回零值。
	log, ch, changed := rateChangeDiff(c, "g1", nil, 2, 3, now)
	if changed || log != nil || ch != (notify.RateChange{}) {
		t.Errorf("no-change path should return zero values, got changed=%v log=%v change=%+v", changed, log, ch)
	}
}

func TestBalanceLowMessage(t *testing.T) {
	for _, tc := range []struct {
		name      string
		threshold float64
		balance   float64
		want      bool
	}{
		{"threshold disabled", 0, 1, false},
		{"negative threshold disabled", -5, -10, false},
		{"balance above threshold", 2, 3, false},
		{"balance equals threshold not low", 2, 2, false},
		{"balance below threshold", 2, 1.5, true},
	} {
		msg, ok := balanceLowMessage(&storage.Channel{ID: 9, Name: "alpha", BalanceThreshold: tc.threshold}, tc.balance)
		if ok != tc.want {
			t.Errorf("%s: ok = %v, want %v", tc.name, ok, tc.want)
			continue
		}
		if !ok {
			continue
		}
		if msg.Event != storage.EventBalanceLow || msg.ChannelID != 9 {
			t.Errorf("%s: msg event/channel = %v/%d", tc.name, msg.Event, msg.ChannelID)
		}
		if msg.Subject != "[upstream-hub] alpha 余额低于阈值" {
			t.Errorf("%s: subject = %q", tc.name, msg.Subject)
		}
		if msg.Body != "当前余额: 1.5000，阈值: 2.0000" {
			t.Errorf("%s: body = %q", tc.name, msg.Body)
		}
	}
}

// 对应 spec：docs/specs/quiet-hours-cooldown.md AC-5。
// 消息构造点把渠道的 notify_cooldowns 解析后随 Message 携带进 Dispatcher；
// 非法 JSON（绕过 API 校验写入的脏数据）fail-open：空覆盖、告警照发。
func TestBalanceLowMessageCarriesCooldown(t *testing.T) {
	ch := &storage.Channel{
		ID:               9,
		Name:             "alpha",
		BalanceThreshold: 2,
		NotifyCooldowns:  `{"balance_low":10,"monitor_failed":5}`,
	}
	msg, ok := balanceLowMessage(ch, 1)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	want := notify.CooldownOverrides{
		storage.EventBalanceLow:    10 * time.Minute,
		storage.EventMonitorFailed: 5 * time.Minute,
	}
	if len(msg.Cooldown) != len(want) {
		t.Fatalf("msg.Cooldown = %v, want %v", msg.Cooldown, want)
	}
	for ev, d := range want {
		if msg.Cooldown[ev] != d {
			t.Errorf("msg.Cooldown[%v] = %v, want %v", ev, msg.Cooldown[ev], d)
		}
	}

	// 未配置 → 空覆盖。
	ch.NotifyCooldowns = ""
	msg, _ = balanceLowMessage(ch, 1)
	if len(msg.Cooldown) != 0 {
		t.Errorf("未配置时 msg.Cooldown = %v, want 空", msg.Cooldown)
	}

	// 非法 JSON → fail-open 空覆盖，消息仍构造。
	ch.NotifyCooldowns = "not json"
	msg, ok = balanceLowMessage(ch, 1)
	if !ok {
		t.Fatal("非法 JSON 时 ok = false, want true（fail-open）")
	}
	if len(msg.Cooldown) != 0 {
		t.Errorf("非法 JSON 时 msg.Cooldown = %v, want 空（回落全局）", msg.Cooldown)
	}
}

// TDD red：balanceStatePlan 尚不存在（P0-3，将在 monitor/changes.go 实现）。
// 对应 spec：docs/specs/balance-recovery.md AC-2 / AC-3。
func TestBalanceStatePlan(t *testing.T) {
	for _, tc := range []struct {
		name    string
		channel storage.Channel // BalanceThreshold / BalanceLowActive / NotifyCooldowns
		balance float64
		enabled bool
		wantLow bool
		wantChg bool
		wantSnd bool
	}{
		{
			name:    "未配置阈值且未标记：什么都不做",
			channel: storage.Channel{ID: 1, BalanceThreshold: 0},
			balance: 5,
			enabled: true,
		},
		{
			name:    "阈值删除后静默清标记（不发通知）",
			channel: storage.Channel{ID: 1, BalanceThreshold: 0, BalanceLowActive: true},
			balance: 5,
			enabled: true,
			wantChg: true, // 清标记
		},
		{
			name:    "首次低于阈值：标记",
			channel: storage.Channel{ID: 1, BalanceThreshold: 10},
			balance: 5,
			enabled: true,
			wantLow: true, wantChg: true,
		},
		{
			name:    "持续低于阈值：状态不变不重复写库",
			channel: storage.Channel{ID: 1, BalanceThreshold: 10, BalanceLowActive: true},
			balance: 5,
			enabled: true,
			wantLow: true,
		},
		{
			name:    "回升但从未标记：无迁移",
			channel: storage.Channel{ID: 1, BalanceThreshold: 10},
			balance: 15,
			enabled: true,
		},
		{
			name:    "回升且已标记但开关关：清标记不发通知",
			channel: storage.Channel{ID: 1, BalanceThreshold: 10, BalanceLowActive: true},
			balance: 15,
			enabled: false,
			wantChg: true,
		},
		{
			name:    "回升且已标记且开关开：发恢复",
			channel: storage.Channel{ID: 1, BalanceThreshold: 10, BalanceLowActive: true},
			balance: 15,
			enabled: true,
			wantChg: true, wantSnd: true,
		},
		{
			name:    "余额恰好等于阈值视为已恢复",
			channel: storage.Channel{ID: 1, BalanceThreshold: 10, BalanceLowActive: true},
			balance: 10,
			enabled: true,
			wantChg: true, wantSnd: true,
		},
		{
			name:    "低于阈值与开关无关（标记始终维护）",
			channel: storage.Channel{ID: 1, BalanceThreshold: 10},
			balance: 5,
			enabled: false,
			wantLow: true, wantChg: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.channel
			plan := balanceStatePlan(&c, tc.balance, tc.enabled)
			if plan.low != tc.wantLow {
				t.Errorf("plan.low = %v, want %v", plan.low, tc.wantLow)
			}
			if plan.changed != tc.wantChg {
				t.Errorf("plan.changed = %v, want %v", plan.changed, tc.wantChg)
			}
			if plan.send != tc.wantSnd {
				t.Errorf("plan.send = %v, want %v", plan.send, tc.wantSnd)
			}
			if !plan.send && plan.message.Event != "" {
				t.Errorf("不派发时 message.Event = %q, want 空", plan.message.Event)
			}
		})
	}
}

// 对应 spec：balance-recovery.md AC-3 消息格式 + AC-2 标记语义
// （标记与告警是否被冷却 / 静默压掉无关，纯函数只看余额与阈值）。
func TestBalanceRecoveryMessage(t *testing.T) {
	c := &storage.Channel{
		ID:               9,
		Name:             "alpha",
		BalanceThreshold: 10,
		BalanceLowActive: true,
		NotifyCooldowns:  `{"balance_low":10}`,
	}
	plan := balanceStatePlan(c, 12.5, true)
	if !plan.send {
		t.Fatal("plan.send = false, want true")
	}
	msg := plan.message
	if msg.Event != storage.EventBalanceRecovered {
		t.Errorf("Event = %q, want balance_recovered", msg.Event)
	}
	if msg.ChannelID != 9 {
		t.Errorf("ChannelID = %d, want 9", msg.ChannelID)
	}
	if msg.Subject != "[upstream-hub] alpha 余额已恢复" {
		t.Errorf("Subject = %q", msg.Subject)
	}
	if msg.Body != "当前余额: 12.5000，阈值: 10.0000" {
		t.Errorf("Body = %q", msg.Body)
	}
	// 照常携带 per-channel 冷却覆盖（Dispatcher 对非冷却事件忽略它）。
	if msg.Cooldown[storage.EventBalanceLow] != 10*time.Minute {
		t.Errorf("msg.Cooldown = %v, want 携带 balance_low 10m", msg.Cooldown)
	}
}
