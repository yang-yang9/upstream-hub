package notify

import (
	"testing"

	"github.com/worryzyy/upstream-hub/internal/storage"
)

// 特征测试：钉住订阅解析与匹配的当前行为。
// 对应 spec：docs/specs/notification-dispatch.md。

func TestParseSubscriptions(t *testing.T) {
	for _, tc := range []struct {
		name      string
		raw       string
		wantNil   bool
		wantLen   int
		wantError bool
	}{
		{"empty string subscribes all", "", true, 0, false},
		{"whitespace only", "   ", true, 0, false},
		{"json null", "null", true, 0, false},
		{"empty array", "[]", false, 0, false},
		{"single subscription", `[{"channel_id":1,"mode":"all"}]`, false, 1, false},
		{"subscription with groups", `[{"channel_id":1,"mode":"groups","groups":["a","b"]}]`, false, 1, false},
		{"multiple subscriptions", `[{"channel_id":1,"mode":"all"},{"channel_id":2,"mode":"all"}]`, false, 2, false},
		{"invalid json", "not-json", true, 0, true},
		{"wrong shape object", `{"channel_id":1}`, true, 0, true},
	} {
		subs, err := ParseSubscriptions(tc.raw)
		if tc.wantError != (err != nil) {
			t.Errorf("%s: err = %v, wantError %v", tc.name, err, tc.wantError)
			continue
		}
		if tc.wantNil && subs != nil {
			t.Errorf("%s: subs = %v, want nil", tc.name, subs)
		}
		if !tc.wantNil && len(subs) != tc.wantLen {
			t.Errorf("%s: len(subs) = %d, want %d", tc.name, len(subs), tc.wantLen)
		}
	}
}

func TestSubscriptionMatches(t *testing.T) {
	rateMsg := Message{Event: storage.EventRateChanged, ChannelID: 1, ModelName: "g1"}
	otherMsg := Message{Event: storage.EventBalanceLow, ChannelID: 1}
	for _, tc := range []struct {
		name string
		sub  Subscription
		msg  Message
		want bool
	}{
		{"all mode matches rate event", Subscription{ChannelID: 1, Mode: SubscriptionModeAll}, rateMsg, true},
		{"all mode matches other event", Subscription{ChannelID: 1, Mode: SubscriptionModeAll}, otherMsg, true},
		{"groups mode group hit", Subscription{ChannelID: 1, Mode: SubscriptionModeGroups, Groups: []string{"g1"}}, rateMsg, true},
		{"groups mode group miss", Subscription{ChannelID: 1, Mode: SubscriptionModeGroups, Groups: []string{"g2"}}, rateMsg, false},
		{"groups mode empty groups", Subscription{ChannelID: 1, Mode: SubscriptionModeGroups}, rateMsg, false},
		{"groups mode ignores group filter for non rate event", Subscription{ChannelID: 1, Mode: SubscriptionModeGroups, Groups: []string{"g2"}}, otherMsg, true},
		{"channel id mismatch", Subscription{ChannelID: 2, Mode: SubscriptionModeAll}, rateMsg, false},
		// 特征：ChannelID=0 的系统消息永不命中任何具体订阅（调用方在 subs 为空时才放行）。
		{"system message never matches", Subscription{ChannelID: 0, Mode: SubscriptionModeAll}, Message{Event: storage.EventBalanceLow}, false},
		{"unknown mode falls back to channel only", Subscription{ChannelID: 1, Mode: SubscriptionMode("weird")}, rateMsg, true},
	} {
		if got := tc.sub.Matches(tc.msg); got != tc.want {
			t.Errorf("%s: Matches = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestAnyMatch(t *testing.T) {
	subs := []Subscription{
		{ChannelID: 1, Mode: SubscriptionModeAll},
		{ChannelID: 2, Mode: SubscriptionModeGroups, Groups: []string{"g1"}},
	}
	if !AnyMatch(subs, Message{Event: storage.EventBalanceLow, ChannelID: 2}) {
		t.Error("second subscription should match")
	}
	if !AnyMatch(subs, Message{Event: storage.EventRateChanged, ChannelID: 2, ModelName: "g1"}) {
		t.Error("groups subscription should match its group")
	}
	if AnyMatch(subs, Message{Event: storage.EventBalanceLow, ChannelID: 3}) {
		t.Error("no subscription covers channel 3")
	}
	// 特征：空切片返回 false；"订阅一切"由调用方在 len==0 时短路处理。
	if AnyMatch(nil, Message{Event: storage.EventBalanceLow, ChannelID: 1}) {
		t.Error("empty subscriptions should not match")
	}
}
