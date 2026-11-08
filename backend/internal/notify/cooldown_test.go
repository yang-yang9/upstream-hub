package notify

import (
	"testing"
	"time"

	"github.com/worryzyy/upstream-hub/internal/storage"
)

// 对应 spec：docs/specs/quiet-hours-cooldown.md AC-4。
// per-channel 冷却覆盖 JSON：键限 4 类参与冷却的事件，值 = 分钟（int >= 0，0 = 不冷却）。
// 空 / {} / 省略 = 不覆盖。非法 JSON / 未知键 / 负数 / 非整数都是错误（API 侧 400）。

func TestParseCooldownOverrides(t *testing.T) {
	min := time.Minute
	for _, tc := range []struct {
		name    string
		in      string
		want    CooldownOverrides
		wantErr bool
	}{
		{"empty string", "", CooldownOverrides{}, false},
		{"empty object", "{}", CooldownOverrides{}, false},
		{"single key", `{"balance_low":10}`, CooldownOverrides{storage.EventBalanceLow: 10 * min}, false},
		{"all four keys", `{"balance_low":10,"balance_forecast_low":30,"login_failed":5,"monitor_failed":0}`,
			CooldownOverrides{
				storage.EventBalanceLow:      10 * min,
				storage.EventBalanceForecast: 30 * min,
				storage.EventLoginFailed:    5 * min,
				storage.EventMonitorFailed:  0, // 0 合法：显式关闭该事件的冷却
			}, false},
		// 非法输入
		{"not json", "not json", nil, true},
		{"trailing comma", `{"balance_low":10,}`, nil, true},
		{"unknown event", `{"rate_changed":10}`, nil, true},
		{"recovered event not cooldown-eligible", `{"login_recovered":10}`, nil, true},
		{"negative", `{"balance_low":-5}`, nil, true},
		{"fractional minutes", `{"balance_low":1.5}`, nil, true},
		{"string value", `{"balance_low":"10"}`, nil, true},
		{"null value", `{"balance_low":null}`, nil, true},
		{"bool value", `{"balance_low":true}`, nil, true},
		{"array", `["balance_low"]`, nil, true},
	} {
		got, err := ParseCooldownOverrides(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: ParseCooldownOverrides(%q) = %v, want error", tc.name, tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: ParseCooldownOverrides(%q) error: %v", tc.name, tc.in, err)
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("%s: got %d entries (%v), want %d (%v)", tc.name, len(got), got, len(tc.want), tc.want)
			continue
		}
		for ev, wantDur := range tc.want {
			if got[ev] != wantDur {
				t.Errorf("%s: overrides[%v] = %v, want %v", tc.name, ev, got[ev], wantDur)
			}
		}
	}
}
