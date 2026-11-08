package notify

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// QuietHours 静默时段（P1-6）。零值 = 未启用（默认 7×24 照发）。
//
// 语义：
//   - 窗口为 [start, end)：start 含、end 不含（分钟粒度）
//   - start > end 表示跨午夜窗口（如 23:00-08:00）：t >= start OR t < end
//   - 时区取服务器本地时区
type QuietHours struct {
	Start int // 一天中的分钟数，[0, 1440)
	End   int
}

// InWindow 时刻 t 是否落在静默窗口内。零值（未启用）永远返回 false。
func (q QuietHours) InWindow(t time.Time) bool {
	if q.Start == q.End {
		return false
	}
	m := t.Hour()*60 + t.Minute()
	if q.Start < q.End {
		return m >= q.Start && m < q.End
	}
	return m >= q.Start || m < q.End // 跨午夜
}

// ParseQuietHours 解析 "HH:MM-HH:MM" 配置。空串返回零值（未启用）。
// 非法格式 / 越界时刻 / start==end 都是错误（配置加载时 fail-fast）。
func ParseQuietHours(raw string) (QuietHours, error) {
	if raw == "" {
		return QuietHours{}, nil
	}
	parts := strings.SplitN(raw, "-", 2)
	if len(parts) != 2 {
		return QuietHours{}, fmt.Errorf("quiet hours %q: want \"HH:MM-HH:MM\"", raw)
	}
	start, err := parseClock(parts[0])
	if err != nil {
		return QuietHours{}, err
	}
	end, err := parseClock(parts[1])
	if err != nil {
		return QuietHours{}, err
	}
	if start == end {
		return QuietHours{}, fmt.Errorf("quiet hours %q: start == end（歧义窗口，如需全时段静默用 00:00-23:59）", raw)
	}
	return QuietHours{Start: start, End: end}, nil
}

// parseClock 解析严格的 "HH:MM"（两位小时、两位分钟）。
func parseClock(s string) (int, error) {
	if len(s) != 5 || s[2] != ':' {
		return 0, fmt.Errorf("quiet hours: invalid clock %q, want HH:MM", s)
	}
	h, err := strconv.Atoi(s[0:2])
	if err != nil {
		return 0, fmt.Errorf("quiet hours: invalid clock %q", s)
	}
	m, err := strconv.Atoi(s[3:5])
	if err != nil {
		return 0, fmt.Errorf("quiet hours: invalid clock %q", s)
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("quiet hours: clock %q out of range", s)
	}
	return h*60 + m, nil
}
