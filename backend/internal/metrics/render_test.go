// /metrics 渲染纯函数单测：Bearer 判定、标签转义、text format 输出。
// 对应 spec：docs/specs/metrics-export.md AC-1/2/4/5 与边界用例（转义、空数据、孤儿日志、排序）。
package metrics

import (
	"strings"
	"testing"
	"time"

	"github.com/worryzyy/upstream-hub/internal/storage"
)

// TestCheckBearer 表驱动覆盖 AC-5：未配置 token 公开；配置后 scheme 大小写不敏感、
// token 区分大小写；缺 scheme / 错 scheme / 多空格 / 尾部空格一律拒绝。
func TestCheckBearer(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
		ok     bool
	}{
		{"未配置 token 时无头放行", "", "", true},
		{"未配置 token 时带头也放行", "Bearer whatever", "", true},
		{"标准 Bearer", "Bearer secret", "secret", true},
		{"scheme 小写", "bearer secret", "secret", true},
		{"scheme 大写", "BEARER secret", "secret", true},
		{"无头", "", "secret", false},
		{"错误 token", "Bearer wrong", "secret", false},
		{"别的 scheme", "Token secret", "secret", false},
		{"裸 token 无 scheme", "secret", "secret", false},
		{"双空格", "Bearer  secret", "secret", false},
		{"尾部空格", "Bearer secret ", "secret", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CheckBearer(tc.header, tc.want); got != tc.ok {
				t.Errorf("CheckBearer(%q, %q) = %v, want %v", tc.header, tc.want, got, tc.ok)
			}
		})
	}
}

// TestEscapeLabelValue text format 标签值转义：`\` `"` 换行。
func TestEscapeLabelValue(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{`we"ird`, `we\"ird`},
		{`back\slash`, `back\\slash`},
		{"new\nline", `new\nline`},
		{`all"of\` + "\n", `all\"of\\\n`},
	}
	for _, tc := range cases {
		if got := escapeLabelValue(tc.in); got != tc.want {
			t.Errorf("escapeLabelValue(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestRender 全量黄金输出：顺序（渠道按 name、job 按字典序）、
// LastBalance 为 nil 的 beta 不出 balance 系列、无日志渠道不出 last_scan 系列。
// 种子刻意打乱渠道顺序（beta 在前）验证排序。
func TestRender(t *testing.T) {
	bal := 87.5
	s := Snapshot{
		Version:   "v1.0.0",
		Commit:    "abc123",
		Scrapes:   2,
		ScrapeErrors: 1,
		Channels: []storage.Channel{
			{ID: 2, Name: "beta", Type: storage.ChannelTypeSub2API, MonitorEnabled: false},
			{ID: 1, Name: "alpha", Type: storage.ChannelTypeNewAPI, MonitorEnabled: true, LastBalance: &bal},
		},
		LatestLogs: []storage.MonitorLog{
			{ChannelID: 1, Job: storage.MonitorJobBalance, Success: true, DurationMS: 1234, StartedAt: time.Unix(1731024000, 0).UTC()},
			{ChannelID: 1, Job: storage.MonitorJobRates, Success: false, DurationMS: 50, StartedAt: time.Unix(1731024001, 0).UTC()},
		},
	}
	want := `# HELP upstreamhub_up Whether the service is up.
# TYPE upstreamhub_up gauge
upstreamhub_up 1
# HELP upstreamhub_build_info Build version information.
# TYPE upstreamhub_build_info gauge
upstreamhub_build_info{version="v1.0.0",commit="abc123"} 1
# HELP upstreamhub_channels_total Number of configured upstream channels.
# TYPE upstreamhub_channels_total gauge
upstreamhub_channels_total 2
# HELP upstreamhub_channel_balance Current balance of the channel.
# TYPE upstreamhub_channel_balance gauge
upstreamhub_channel_balance{name="alpha",type="newapi"} 87.5
# HELP upstreamhub_channel_monitor_enabled Whether monitoring is enabled for the channel (1/0).
# TYPE upstreamhub_channel_monitor_enabled gauge
upstreamhub_channel_monitor_enabled{name="alpha"} 1
upstreamhub_channel_monitor_enabled{name="beta"} 0
# HELP upstreamhub_channel_last_scan_success Whether the latest scan of the job succeeded (1/0).
# TYPE upstreamhub_channel_last_scan_success gauge
upstreamhub_channel_last_scan_success{name="alpha",job="balance"} 1
upstreamhub_channel_last_scan_success{name="alpha",job="rates"} 0
# HELP upstreamhub_channel_last_scan_duration_ms Duration of the latest scan of the job in milliseconds.
# TYPE upstreamhub_channel_last_scan_duration_ms gauge
upstreamhub_channel_last_scan_duration_ms{name="alpha",job="balance"} 1234
upstreamhub_channel_last_scan_duration_ms{name="alpha",job="rates"} 50
# HELP upstreamhub_channel_last_scan_started_at Unix timestamp (seconds) of the latest scan of the channel job.
# TYPE upstreamhub_channel_last_scan_started_at gauge
upstreamhub_channel_last_scan_started_at{name="alpha",job="balance"} 1731024000
upstreamhub_channel_last_scan_started_at{name="alpha",job="rates"} 1731024001
# HELP upstreamhub_metrics_scrapes_total Total successful /metrics scrapes.
# TYPE upstreamhub_metrics_scrapes_total counter
upstreamhub_metrics_scrapes_total 2
# HELP upstreamhub_metrics_scrape_errors_total Total /metrics scrapes that failed to read the database.
# TYPE upstreamhub_metrics_scrape_errors_total counter
upstreamhub_metrics_scrape_errors_total 1
`
	if got := Render(s); got != want {
		t.Fatalf("Render 输出与黄金串不符：\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestRenderEmpty 空库：只有进程级指标，channels_total 0，无任何渠道系列（边界用例）。
func TestRenderEmpty(t *testing.T) {
	got := Render(Snapshot{Version: "v0.0.0", Commit: "dev"})
	for _, line := range []string{
		"upstreamhub_up 1",
		`upstreamhub_build_info{version="v0.0.0",commit="dev"} 1`,
		"upstreamhub_channels_total 0",
		"upstreamhub_metrics_scrapes_total 0",
		"upstreamhub_metrics_scrape_errors_total 0",
	} {
		if !strings.Contains(got, line) {
			t.Errorf("空快照缺进程级行 %q，输出：\n%s", line, got)
		}
	}
	// 注意判"无渠道系列"要查数据行而不是族名：HELP/TYPE 头本身就含 channel_balance 字样。
	if strings.Contains(got, `name="`) {
		t.Errorf("空快照不应有渠道系列，输出：\n%s", got)
	}
}

// TestRenderEscapesLabelValues 渠道名带特殊字符时标签值必须转义（边界用例）。
func TestRenderEscapesLabelValues(t *testing.T) {
	got := Render(Snapshot{
		Version: "v1", Commit: "c",
		Channels: []storage.Channel{{ID: 1, Name: "we\"ird\\\nx", Type: storage.ChannelTypeNewAPI, MonitorEnabled: true}},
	})
	if !strings.Contains(got, `upstreamhub_channel_monitor_enabled{name="we\"ird\\\nx"} 1`) {
		t.Errorf("标签值未按 text format 转义，输出：\n%s", got)
	}
}

// TestRenderSkipsOrphanLogs 日志指向不在 Channels 里的渠道（如已软删除）时，
// 对应 last_scan 系列不输出（边界用例：软删除渠道不出现）。
func TestRenderSkipsOrphanLogs(t *testing.T) {
	got := Render(Snapshot{
		Version: "v1", Commit: "c",
		Channels: []storage.Channel{{ID: 1, Name: "live", Type: storage.ChannelTypeNewAPI, MonitorEnabled: true}},
		LatestLogs: []storage.MonitorLog{
			{ChannelID: 99, Job: storage.MonitorJobBalance, Success: true, DurationMS: 1, StartedAt: time.Unix(1731024000, 0).UTC()},
		},
	})
	if strings.Contains(got, "job=") {
		t.Errorf("孤儿渠道的日志不应渲染 last_scan 系列，输出：\n%s", got)
	}
}
