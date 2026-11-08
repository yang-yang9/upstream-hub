// 白盒测试：SPA fallback 的保留路径判定。
// 对应 spec：docs/specs/metrics-export.md AC-7（/metrics 与 /api/*、/healthz 同列不被回落 index.html）。
package api

import "testing"

func TestReservedPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/api/channels", true},
		{"/api/", true},
		{"/healthz", true},
		{"/metrics", true},
		{"/metricsx", false},
		{"/channels", false},
		{"/", false},
	}
	for _, tc := range cases {
		if got := reservedPath(tc.path); got != tc.want {
			t.Errorf("reservedPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
