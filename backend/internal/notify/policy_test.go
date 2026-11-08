package notify

import (
	"strings"
	"testing"

	"github.com/worryzyy/upstream-hub/internal/storage"
)

// 特征测试：钉住 Policy 纯函数的当前行为，作为后续重构的行为基线。
// 对应 spec：docs/specs/notification-dispatch.md。

func TestChangePctAbove(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change RateChange
		minPct float64
		want   bool
	}{
		{"minPct zero disables filter", RateChange{OldRatio: 2, NewRatio: 2.0001}, 0, true},
		{"negative minPct disables filter", RateChange{OldRatio: 2, NewRatio: 2}, -1, true},
		{"first seen old zero always above", RateChange{OldRatio: 0, NewRatio: 0}, 10, true},
		{"old zero with minPct zero", RateChange{OldRatio: 0, NewRatio: 5}, 0, true},
		{"exact threshold is inclusive", RateChange{OldRatio: 100, NewRatio: 110}, 10, true},
		{"just below threshold", RateChange{OldRatio: 100, NewRatio: 109.9}, 10, false},
		{"increase above threshold", RateChange{OldRatio: 2, NewRatio: 3}, 25, true},
		{"decrease counts by absolute pct", RateChange{OldRatio: 4, NewRatio: 3}, 25, true},
		{"decrease just below", RateChange{OldRatio: 4, NewRatio: 3.1}, 25, false},
		{"no change not above positive minPct", RateChange{OldRatio: 3, NewRatio: 3}, 0.1, false},
		{"negative ratios use absolute denominator", RateChange{OldRatio: -2, NewRatio: -3}, 25, true},
	} {
		if got := tc.change.ChangePctAbove(tc.minPct); got != tc.want {
			t.Errorf("%s: ChangePctAbove(%v) = %v, want %v", tc.name, tc.minPct, got, tc.want)
		}
	}
}

func TestBuildBatchMessageSingle(t *testing.T) {
	ch := &storage.Channel{ID: 7, Name: "alpha"}
	c := RateChange{GroupName: "default", OldRatio: 2, NewRatio: 3, OldComp: 4, NewComp: 5}
	msg := BuildBatchMessage(ch, []RateChange{c})

	if msg.Event != storage.EventRateChanged {
		t.Errorf("event = %v, want rate_changed", msg.Event)
	}
	if msg.ChannelID != 7 {
		t.Errorf("channel id = %d, want 7", msg.ChannelID)
	}
	if msg.ModelName != "default" {
		t.Errorf("model name = %q, want default", msg.ModelName)
	}
	if !strings.Contains(msg.Subject, "alpha") || !strings.Contains(msg.Subject, "default") {
		t.Errorf("subject %q missing channel or group", msg.Subject)
	}
	// 特征：单条 body 只展示分组倍率，completion ratio（OldComp/NewComp）不出现。
	wantPrefix := "渠道：alpha\n分组倍率：default 由 2 上涨至 3\n变化时间："
	if !strings.HasPrefix(msg.Body, wantPrefix) {
		t.Errorf("body %q should start with %q", msg.Body, wantPrefix)
	}
}

func TestBuildBatchMessageMulti(t *testing.T) {
	ch := &storage.Channel{ID: 1, Name: "beta"}
	changes := []RateChange{
		{GroupName: "g1", OldRatio: 1, NewRatio: 2},
		{GroupName: "g2", OldRatio: 3, NewRatio: 2.5},
	}
	msg := BuildBatchMessage(ch, changes)

	if msg.ModelName != "" {
		t.Errorf("merged message ModelName = %q, want empty", msg.ModelName)
	}
	if !strings.Contains(msg.Subject, "2 个分组变动") {
		t.Errorf("subject %q missing count", msg.Subject)
	}
	for _, want := range []string{"beta", "g1", "g2", "上涨", "下调", "共 2 个分组倍率变化"} {
		if !strings.Contains(msg.Body, want) {
			t.Errorf("body %q missing %q", msg.Body, want)
		}
	}
}

func TestBuildBatchMessageEmpty(t *testing.T) {
	msg := BuildBatchMessage(&storage.Channel{ID: 1}, nil)
	if msg.Event != "" || msg.ChannelID != 0 || msg.ModelName != "" ||
		msg.Subject != "" || msg.Body != "" || msg.Extra != nil {
		t.Errorf("empty input should produce zero Message, got %+v", msg)
	}
}

func TestArrowFor(t *testing.T) {
	for _, tc := range []struct {
		oldV, newV float64
		want       string
	}{
		{1, 2, "上涨"}, {2, 1, "下调"}, {1, 1, "调整"},
	} {
		if got := arrowFor(tc.oldV, tc.newV); got != tc.want {
			t.Errorf("arrowFor(%v, %v) = %q, want %q", tc.oldV, tc.newV, got, tc.want)
		}
	}
}
