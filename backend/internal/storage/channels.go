package storage

import (
	"gorm.io/gorm"
)

// Channels 渠道仓库。
type Channels struct{ db *gorm.DB }

func NewChannels(db *gorm.DB) *Channels { return &Channels{db: db} }

func (r *Channels) Create(c *Channel) error { return r.db.Create(c).Error }
func (r *Channels) Update(c *Channel) error { return r.db.Save(c).Error }
func (r *Channels) Delete(id uint) error    { return r.db.Delete(&Channel{}, id).Error }
func (r *Channels) FindByID(id uint) (*Channel, error) {
	var c Channel
	if err := r.db.First(&c, id).Error; err != nil {
		return nil, err
	}
	return &c, nil
}
func (r *Channels) List() ([]Channel, error) {
	var list []Channel
	if err := r.db.Order("id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}
func (r *Channels) ListMonitorEnabled() ([]Channel, error) {
	var list []Channel
	if err := r.db.Where("monitor_enabled = ?", true).Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}
func (r *Channels) UpdateBalance(id uint, balance float64, at any, lastErr string) error {
	return r.db.Model(&Channel{}).Where("id = ?", id).Updates(map[string]any{
		"last_balance":    balance,
		"last_balance_at": at,
		"last_error":      lastErr,
	}).Error
}
func (r *Channels) SetLastError(id uint, msg string) error {
	return r.db.Model(&Channel{}).Where("id = ?", id).Update("last_error", msg).Error
}

// SetBalanceLowActive 迁移低余额告警状态（P0-3，docs/specs/balance-recovery.md）。
// 只更新该列；调用方（monitor）只在状态翻转时写，避免每次扫描都落库。
func (r *Channels) SetBalanceLowActive(id uint, active bool) error {
	return r.db.Model(&Channel{}).Where("id = ?", id).Update("balance_low_active", active).Error
}

// RotateTokenCredential only replaces the credentials observed before refresh.
// A concurrent manual edit must never be overwritten by a delayed response.
func (r *Channels) RotateTokenCredential(id uint, previous, next string) (bool, error) {
	result := r.db.Model(&Channel{}).Where("id = ? AND credential_mode = ? AND password_cipher = ? AND deleted_at IS NULL", id, CredentialModeToken, previous).Update("password_cipher", next)
	return result.RowsAffected == 1, result.Error
}
