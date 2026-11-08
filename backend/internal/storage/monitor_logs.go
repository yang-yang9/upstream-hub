package storage

import (
	"time"

	"gorm.io/gorm"
)

type MonitorLogs struct{ db *gorm.DB }

func NewMonitorLogs(db *gorm.DB) *MonitorLogs { return &MonitorLogs{db: db} }

func (r *MonitorLogs) Append(l *MonitorLog) error {
	// 同一个 now 填两个零值：两次 time.Now() 若跨毫秒边界，
	// 自动算出的 DurationMS 会偶发为 1。
	now := time.Now()
	if l.StartedAt.IsZero() {
		l.StartedAt = now
	}
	if l.FinishedAt.IsZero() {
		l.FinishedAt = now
	}
	if l.DurationMS == 0 {
		l.DurationMS = l.FinishedAt.Sub(l.StartedAt).Milliseconds()
	}
	return r.db.Create(l).Error
}

// List 倒序拉取监控日志。channelID 为 0 表示不过滤。
func (r *MonitorLogs) List(channelID uint, limit int) ([]MonitorLog, error) {
	if limit <= 0 {
		limit = 100
	}
	q := r.db.Order("started_at DESC").Limit(limit)
	if channelID != 0 {
		q = q.Where("channel_id = ?", channelID)
	}
	var list []MonitorLog
	if err := q.Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// DeleteBefore 删除 started_at < cutoff 的日志，返回删除行数。
func (r *MonitorLogs) DeleteBefore(cutoff time.Time) (int64, error) {
	res := r.db.Where("started_at < ?", cutoff).Delete(&MonitorLog{})
	return res.RowsAffected, res.Error
}

// LatestPerChannel 每 (channel_id, job) 取 started_at 最新的一条，按 (channel_id, job) 升序返回。
// DISTINCT ON 是 PostgreSQL 特有语法（同 rates.go 的 date_trunc 聚合，只在真 PG 集成测试覆盖）。
// 软删除渠道的日志仍会返回——"软删除渠道不出指标"的过滤在渲染层做渠道 join（metrics.Render）。
func (r *MonitorLogs) LatestPerChannel() ([]MonitorLog, error) {
	var list []MonitorLog
	err := r.db.
		Raw(`SELECT DISTINCT ON (channel_id, job) * FROM monitor_logs ORDER BY channel_id, job, started_at DESC`).
		Scan(&list).Error
	return list, err
}
