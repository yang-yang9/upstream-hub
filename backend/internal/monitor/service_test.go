package monitor

import (
	"sync"
	"testing"
)

// TDD red：Service.UpdateSettings / settingsSnapshot 尚不存在
// （将在 monitor/service.go 实现）。对应 spec：docs/specs/runtime-settings.md AC-4。
// 构造器签名不变，热更新走推送式 Update；读取统一走快照（读写锁并发安全）。

func TestServiceUpdateSettings(t *testing.T) {
	s := NewService(nil, nil, nil, nil, nil, nil, ForecastConfig{WindowDays: 7, AlertDays: 7}, nil, false)

	if fc, br := s.settingsSnapshot(); fc.WindowDays != 7 || fc.AlertDays != 7 || br {
		t.Errorf("初始快照 = (%+v, %v), want 构造器传入值", fc, br)
	}

	s.UpdateSettings(ForecastConfig{WindowDays: 3, AlertDays: 2}, true)

	if fc, br := s.settingsSnapshot(); fc.WindowDays != 3 || fc.AlertDays != 2 || !br {
		t.Errorf("更新后快照 = (%+v, %v), want (WindowDays=3, AlertDays=2, true)", fc, br)
	}
}

// -race 下并发 UpdateSettings + settingsSnapshot 不出竞态
// （对应边界用例「PUT 与扫描并发」）。
func TestServiceUpdateSettingsConcurrent(t *testing.T) {
	s := NewService(nil, nil, nil, nil, nil, nil, ForecastConfig{WindowDays: 7, AlertDays: 7}, nil, false)

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				s.UpdateSettings(ForecastConfig{WindowDays: i + 1, AlertDays: 7}, j%2 == 0)
			}
		}(i)
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				fc, _ := s.settingsSnapshot()
				if fc.WindowDays <= 0 || fc.WindowDays > 4 {
					t.Errorf("快照 WindowDays = %d, want 1..4", fc.WindowDays)
				}
			}
		}()
	}
	wg.Wait()
}
