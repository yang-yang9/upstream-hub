// Package metrics 渲染 Prometheus text format 的 /metrics 输出（docs/specs/metrics-export.md）。
//
// 刻意零依赖手写而不是引 prometheus/client_golang：指标集固定且小（gauge + counter），
// 渲染是纯函数，可表驱动单测；text format 0.0.4 语法足够覆盖。
package metrics

import (
	"crypto/subtle"
	"sort"
	"strconv"
	"strings"

	"github.com/worryzyy/upstream-hub/internal/storage"
)

// Snapshot 渲染一次 /metrics 输出所需的数据快照。handler 即时查库填充（spec 设计决策：
// scrape 间隔通常 ≥15s，两条轻查询无压力；业务数据不落内存，重启不丢）。
// 进程内计数器（Scrapes / ScrapeErrors）重启清零，属可接受语义。
type Snapshot struct {
	Version      string
	Commit       string
	Channels     []storage.Channel
	LatestLogs   []storage.MonitorLog // 每 (channel_id, job) 最新一条
	Scrapes      uint64
	ScrapeErrors uint64
}

// CheckBearer 校验 Authorization 头。want 为空表示端点公开，恒放行；
// 否则要求 "Bearer <token>"：scheme 大小写不敏感，token 区分大小写且恒定时间比较
//（防止时序侧信道逐字节猜测）。
func CheckBearer(header, want string) bool {
	if want == "" {
		return true
	}
	const scheme = "Bearer "
	if len(header) <= len(scheme) || !strings.EqualFold(header[:len(scheme)], scheme) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(header[len(scheme):]), []byte(want)) == 1
}

// Render 渲染 text format。输出确定性：渠道系列按 name 排序、job 系列按 (name, job) 排序。
// 日志指向快照中不存在的渠道（如已软删除）时跳过对应 series。
func Render(s Snapshot) string {
	var b strings.Builder

	chans := append([]storage.Channel(nil), s.Channels...)
	sort.Slice(chans, func(i, j int) bool { return chans[i].Name < chans[j].Name })

	logsByChannel := map[uint][]storage.MonitorLog{}
	present := map[uint]struct{}{}
	for _, c := range chans {
		present[c.ID] = struct{}{}
	}
	for _, l := range s.LatestLogs {
		if _, ok := present[l.ChannelID]; !ok {
			continue // 渠道不在列表（已软删除）：跳过
		}
		logsByChannel[l.ChannelID] = append(logsByChannel[l.ChannelID], l)
	}
	for _, logs := range logsByChannel {
		sort.Slice(logs, func(i, j int) bool { return logs[i].Job < logs[j].Job })
	}

	family(&b, "upstreamhub_up", "Whether the service is up.", "gauge")
	b.WriteString("upstreamhub_up 1\n")

	family(&b, "upstreamhub_build_info", "Build version information.", "gauge")
	b.WriteString(`upstreamhub_build_info` + labels("version", s.Version, "commit", s.Commit) + " 1\n")

	family(&b, "upstreamhub_channels_total", "Number of configured upstream channels.", "gauge")
	b.WriteString("upstreamhub_channels_total " + strconv.Itoa(len(chans)) + "\n")

	family(&b, "upstreamhub_channel_balance", "Current balance of the channel.", "gauge")
	for _, c := range chans {
		if c.LastBalance == nil {
			continue // 从未采集成功过：跳过该 series
		}
		b.WriteString(`upstreamhub_channel_balance` + labels("name", c.Name, "type", string(c.Type)) + " " + formatValue(*c.LastBalance) + "\n")
	}

	family(&b, "upstreamhub_channel_monitor_enabled", "Whether monitoring is enabled for the channel (1/0).", "gauge")
	for _, c := range chans {
		b.WriteString(`upstreamhub_channel_monitor_enabled` + labels("name", c.Name) + " " + boolValue(c.MonitorEnabled) + "\n")
	}

	family(&b, "upstreamhub_channel_last_scan_success", "Whether the latest scan of the job succeeded (1/0).", "gauge")
	eachLatest(&b, "upstreamhub_channel_last_scan_success", chans, logsByChannel, func(l storage.MonitorLog) string { return boolValue(l.Success) })

	family(&b, "upstreamhub_channel_last_scan_duration_ms", "Duration of the latest scan of the job in milliseconds.", "gauge")
	eachLatest(&b, "upstreamhub_channel_last_scan_duration_ms", chans, logsByChannel, func(l storage.MonitorLog) string { return formatValue(float64(l.DurationMS)) })

	family(&b, "upstreamhub_channel_last_scan_started_at", "Unix timestamp (seconds) of the latest scan of the channel job.", "gauge")
	eachLatest(&b, "upstreamhub_channel_last_scan_started_at", chans, logsByChannel, func(l storage.MonitorLog) string { return strconv.FormatInt(l.StartedAt.Unix(), 10) })

	family(&b, "upstreamhub_metrics_scrapes_total", "Total successful /metrics scrapes.", "counter")
	b.WriteString("upstreamhub_metrics_scrapes_total " + strconv.FormatUint(s.Scrapes, 10) + "\n")

	family(&b, "upstreamhub_metrics_scrape_errors_total", "Total /metrics scrapes that failed to read the database.", "counter")
	b.WriteString("upstreamhub_metrics_scrape_errors_total " + strconv.FormatUint(s.ScrapeErrors, 10) + "\n")

	return b.String()
}

// eachLatest 按（渠道 name、job 字典序）遍历每渠道每 job 的最新日志，输出同一指标族的一行。
func eachLatest(b *strings.Builder, name string, chans []storage.Channel, logsByChannel map[uint][]storage.MonitorLog, value func(storage.MonitorLog) string) {
	for _, c := range chans {
		for _, l := range logsByChannel[c.ID] {
			b.WriteString(name + labels("name", c.Name, "job", string(l.Job)) + " " + value(l) + "\n")
		}
	}
}

// family 输出指标族的 HELP / TYPE 头。
func family(b *strings.Builder, name, help, typ string) {
	b.WriteString("# HELP " + name + " " + help + "\n")
	b.WriteString("# TYPE " + name + " " + typ + "\n")
}

// labels 构造 {k="v",...}；pairs 为空返回空串（无标签指标）。
func labels(pairs ...string) string {
	if len(pairs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i+1 < len(pairs); i += 2 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(pairs[i])
		b.WriteString(`="`)
		b.WriteString(escapeLabelValue(pairs[i+1]))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

// escapeLabelValue 转义 text format 标签值里的 `\` `"` 换行。
// strings.NewReplacer 单趟替换，不会把先替换出来的 `\` 再次转义。
func escapeLabelValue(v string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v)
}

// formatValue 数值用 'f' 而不是 'g'：'g' 会把 Unix 时间戳渲染成 1.731024e+09（合法但难读）。
func formatValue(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func boolValue(v bool) string {
	if v {
		return "1"
	}
	return "0"
}
