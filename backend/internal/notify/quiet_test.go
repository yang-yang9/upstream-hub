package notify

import (
	"testing"
	"time"
)

// 对应 spec：docs/specs/quiet-hours-cooldown.md AC-2。
// 静默窗口 "HH:MM-HH:MM" 解析：空 = 关闭（零值），跨午夜合法，
// start==end / 非法时刻 / 非 HH:MM 都是配置错误。

func TestParseQuietHours(t *testing.T) {
	for _, tc := range []struct {
		in       string
		wantErr  bool
		wantZero bool // 解析结果是否为未启用零值
	}{
		{in: "", wantZero: true},
		{"23:00-08:00", false, false},
		{"08:00-23:00", false, false},
		{"00:00-23:59", false, false},
		// 非法：格式 / 越界 / start==end
		{"8:00-23:00", true, false},   // 非 HH:MM
		{"23:0-08:00", true, false},   // 非 MM 两位
		{"25:00-06:00", true, false},  // 小时越界
		{"23:60-01:00", true, false},  // 分钟越界
		{"-1:00-06:00", true, false},  // 负数
		{"23:00", true, false},        // 缺少 end
		{"23:00-08:00-09:00", true, false}, // 多段
		{"23:00-23:00", true, false},  // start==end 歧义，拒绝
		{"00:00-00:00", true, false},
		{"abc-def", true, false},
		{"2300-0800", true, false},    // 缺冒号
		{"23:00 - 08:00", true, false}, // 带空格
	} {
		q, err := ParseQuietHours(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseQuietHours(%q) = %+v, want error", tc.in, q)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseQuietHours(%q) error: %v", tc.in, err)
			continue
		}
		if tc.wantZero {
			if q != (QuietHours{}) {
				t.Errorf("ParseQuietHours(%q) = %+v, want 零值（未启用）", tc.in, q)
			}
		}
	}
}

// 边界语义：[start, end)。start 含、end 不含；跨午夜窗口 = t >= start OR t < end。
// 服务器本地时区，Date 无关。
func TestQuietHoursInWindow(t *testing.T) {
	at := func(h, m, s int) time.Time { return time.Date(2026, 1, 1, h, m, s, 0, time.Local) }

	cross, err := ParseQuietHours("23:00-08:00")
	if err != nil {
		t.Fatalf("parse cross-midnight: %v", err)
	}
	for _, tc := range []struct {
		t    time.Time
		want bool
	}{
		{at(23, 0, 0), true},  // start 含
		{at(23, 59, 59), true},
		{at(0, 0, 0), true},   // 午夜后仍在窗口
		{at(3, 30, 0), true},
		{at(7, 59, 59), true},
		{at(8, 0, 0), false},  // end 不含
		{at(12, 0, 0), false},
		{at(22, 59, 59), false},
	} {
		if got := cross.InWindow(tc.t); got != tc.want {
			t.Errorf("cross 23:00-08:00 InWindow(%v) = %v, want %v", tc.t, got, tc.want)
		}
	}

	normal, err := ParseQuietHours("08:00-23:00")
	if err != nil {
		t.Fatalf("parse normal: %v", err)
	}
	for _, tc := range []struct {
		t    time.Time
		want bool
	}{
		{at(8, 0, 0), true},
		{at(12, 0, 0), true},
		{at(22, 59, 59), true},
		{at(23, 0, 0), false}, // end 不含
		{at(7, 59, 59), false},
		{at(0, 0, 0), false},
	} {
		if got := normal.InWindow(tc.t); got != tc.want {
			t.Errorf("normal 08:00-23:00 InWindow(%v) = %v, want %v", tc.t, got, tc.want)
		}
	}

	// 零值（未配置）永不静默。
	var disabled QuietHours
	if disabled.InWindow(at(3, 0, 0)) {
		t.Error("零值 QuietHours InWindow = true, want false（默认关闭）")
	}
}
