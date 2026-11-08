package storage

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// Failures 连续失败 streak 仓库（表 failure_streaks）。
type Failures struct{ db *gorm.DB }

func NewFailures(db *gorm.DB) *Failures { return &Failures{db: db} }

// RecordFailure 原子递增 (channelID, job) 的连续失败计数，返回递增后的值。
//
// 首次失败写入 count=1 与 first_failed_at / last_failed_at；后续只递增 count、
// 刷新 last_failed_at，first_failed_at 首写后不变（本周期第一次失败的时点）。
// 并发安全靠 PostgreSQL 的 INSERT ... ON CONFLICT DO UPDATE RETURNING 一句 SQL
// （与 TryClaimCooldown 同模式），两个 goroutine 同时递增不会丢计数。
func (r *Failures) RecordFailure(channelID uint, job MonitorJob) (int64, error) {
	now := time.Now()
	var count int64
	err := r.db.Raw(`
		INSERT INTO failure_streaks (channel_id, job, count, first_failed_at, last_failed_at, updated_at)
		VALUES (?, ?, 1, ?, ?, ?)
		ON CONFLICT (channel_id, job) DO UPDATE
		SET count         = failure_streaks.count + 1,
		    last_failed_at = EXCLUDED.last_failed_at,
		    updated_at     = EXCLUDED.updated_at
		RETURNING count
	`, channelID, string(job), now, now, now).Scan(&count).Error
	return count, err
}

// TakeStreak 读取并删除 (channelID, job) 的 streak；不存在返回 (nil, nil)。
// 读取成功但删除失败时返回错误——此时 streak 留在库里，调用方下次成功会再取一次。
func (r *Failures) TakeStreak(channelID uint, job MonitorJob) (*FailureStreak, error) {
	var s FailureStreak
	err := r.db.Where("channel_id = ? AND job = ?", channelID, string(job)).First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := r.db.Delete(&s).Error; err != nil {
		return nil, err
	}
	return &s, nil
}
