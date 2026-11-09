package settings

import (
	"sync"

	"github.com/worryzyy/upstream-hub/internal/storage"
)

// Store app_settings 单行仓储的窄接口。*storage.AppSettingsRepo 天然满足；
// 测试用手写 fake（仓库惯例：不用 mock 库）。
type Store interface {
	Get() (*storage.AppSettings, error)
	Save(s *storage.AppSettings) error
	Delete() error
}

// Service 运行时配置的单一事实源：进程内当前生效值 + saved 标记 +
// 变更订阅（OnApply）。优先级：app_settings 行 > env / config.yaml 推导基线
//（spec AC-1）；Update 先校验后落库再生效，Reset 删行回退基线（AC-4）。
type Service struct {
	store Store
	base  storage.AppSettings

	mu       sync.RWMutex
	cur      storage.AppSettings // 当前生效值（Init / Update / Reset 换入）
	saved    bool                // app_settings 是否存在行
	appliers []func(storage.AppSettings)
}

// NewService 构造即以基线生效（Init 前的 Current 不会返回零值），
// 随后 Init 用库行覆盖（如有）。
func NewService(store Store, base storage.AppSettings) *Service {
	return &Service{store: store, base: base, cur: base}
}

// Init 启动时装载：有行以行为准，无行用 env 推导基线（零行为变化）。
// 读库失败返回错误，由调用方决定是否继续启动。
func (s *Service) Init() error {
	row, err := s.store.Get()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if row != nil {
		s.cur = *row
		s.saved = true
	} else {
		s.cur = s.base
		s.saved = false
	}
	return nil
}

// Current 返回当前生效配置的拷贝。
func (s *Service) Current() storage.AppSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

// Saved 报告 app_settings 是否存在已保存行（GET /api/settings 的 saved 字段）。
func (s *Service) Saved() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.saved
}

// OnApply 订阅配置变更（含 Reset 回退）。须在 Init 后、组件启动前注册。
func (s *Service) OnApply(f func(storage.AppSettings)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appliers = append(s.appliers, f)
}

// Update 校验 → 落库 → 换入生效值 → 推送订阅。任一步失败即返回错误且
// 不产生部分生效（spec 边界用例：落库失败不生效）。
func (s *Service) Update(v storage.AppSettings) error {
	if err := Validate(v); err != nil {
		return err
	}
	if err := s.store.Save(&v); err != nil {
		return err
	}
	s.mu.Lock()
	s.cur = v
	s.saved = true
	for _, f := range s.appliers {
		f(v)
	}
	s.mu.Unlock()
	return nil
}

// Reset 删行回退 env / config.yaml 基线。删行失败保持当前值不动（不半途回退）。
func (s *Service) Reset() error {
	if err := s.store.Delete(); err != nil {
		return err
	}
	s.mu.Lock()
	s.cur = s.base
	s.saved = false
	for _, f := range s.appliers {
		f(s.base)
	}
	s.mu.Unlock()
	return nil
}
