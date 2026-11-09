// appsettings.go 运行时配置单行表仓储（P3-14，docs/specs/runtime-settings.md）。
package storage

import (
	"errors"

	"gorm.io/gorm"
)

// AppSettingsRepo app_settings 表的读写。单行语义：Save 永远写 id=1 那一行
//（gorm Save 按 primaryKey upsert），Get 无行返回 (nil, nil)——调用方
//（settings.Service）据此回退 env / config.yaml 推导值。
type AppSettingsRepo struct {
	db *gorm.DB
}

func NewAppSettings(db *gorm.DB) *AppSettingsRepo {
	return &AppSettingsRepo{db: db}
}

// Get 读取运行时配置行；表为空时返回 (nil, nil) 表示"从未保存过"。
func (r *AppSettingsRepo) Get() (*AppSettings, error) {
	var row AppSettings
	err := r.db.First(&row, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// Save 保存（覆盖）单行配置。id 固定为 1：这是单行表，不接受调用方指定其他行。
func (r *AppSettingsRepo) Save(s *AppSettings) error {
	s.ID = 1
	return r.db.Save(s).Error
}

// Delete 删除配置行，回到"从未保存过"（env / config.yaml 重新接管）。
// 无行时删除不报错（幂等）。
func (r *AppSettingsRepo) Delete() error {
	return r.db.Delete(&AppSettings{}, 1).Error
}
