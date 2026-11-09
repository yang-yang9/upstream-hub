package settings

import (
	"errors"
	"reflect"
	"testing"

	"github.com/worryzyy/upstream-hub/internal/storage"
)

// TDD red：Service 尚不存在（将在 internal/settings/service.go 实现）。
// 对应 spec：docs/specs/runtime-settings.md AC-1（优先级）/ AC-4（热生效）/
// 边界用例「重置失败不半途回退」。

// fakeStore 是 Store 的内存假实现（单行）。
type fakeStore struct {
	row     *storage.AppSettings
	getErr  error
	saveErr error
	delErr  error
	saved   []*storage.AppSettings
	deleted int
}

func (f *fakeStore) Get() (*storage.AppSettings, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.row, nil
}

func (f *fakeStore) Save(s *storage.AppSettings) error {
	f.saved = append(f.saved, s)
	if f.saveErr != nil {
		return f.saveErr
	}
	f.row = s
	return nil
}

func (f *fakeStore) Delete() error {
	f.deleted++
	if f.delErr != nil {
		return f.delErr
	}
	f.row = nil
	return nil
}

func TestServiceInit(t *testing.T) {
	base := FromConfig(testConfig())

	t.Run("无行：Current 回退 env 推导值", func(t *testing.T) {
		s := NewService(&fakeStore{}, base)
		if err := s.Init(); err != nil {
			t.Fatalf("Init: %v", err)
		}
		if got := s.Current(); !reflect.DeepEqual(got, base) {
			t.Errorf("Current:\n got  %+v\n want base %+v", got, base)
		}
	})

	t.Run("有行：Current 以行为准（env 被忽略）", func(t *testing.T) {
		row := base
		row.BalanceCron = "0 0 5 * * *"
		row.FailureThreshold = 7
		s := NewService(&fakeStore{row: &row}, base)
		if err := s.Init(); err != nil {
			t.Fatalf("Init: %v", err)
		}
		if got := s.Current(); got.BalanceCron != "0 0 5 * * *" || got.FailureThreshold != 7 {
			t.Errorf("Current 应取库值，got %+v", got)
		}
	})

	t.Run("读行报错：Init 返回错误", func(t *testing.T) {
		s := NewService(&fakeStore{getErr: errors.New("db down")}, base)
		if err := s.Init(); err == nil {
			t.Error("Init 应返回存储错误")
		}
	})
}

func TestServiceUpdate(t *testing.T) {
	base := FromConfig(testConfig())

	t.Run("非法输入：不落库、不通知订阅、Current 不变", func(t *testing.T) {
		f := &fakeStore{}
		s := NewService(f, base)
		var applied []storage.AppSettings
		s.OnApply(func(v storage.AppSettings) { applied = append(applied, v) })

		bad := base
		bad.FailureThreshold = 0
		if err := s.Update(bad); err == nil {
			t.Fatal("Update 非法输入应报错")
		}
		if len(f.saved) != 0 {
			t.Errorf("不应落库，got %d 次 Save", len(f.saved))
		}
		if len(applied) != 0 {
			t.Errorf("不应触发订阅，got %d 次", len(applied))
		}
		if got := s.Current(); !reflect.DeepEqual(got, base) {
			t.Errorf("Current 应保持旧值，got %+v", got)
		}
	})

	t.Run("落库失败：不生效、不通知订阅", func(t *testing.T) {
		f := &fakeStore{saveErr: errors.New("db down")}
		s := NewService(f, base)
		var applied []storage.AppSettings
		s.OnApply(func(v storage.AppSettings) { applied = append(applied, v) })

		good := base
		good.BalanceCron = "0 0 5 * * *"
		if err := s.Update(good); err == nil {
			t.Fatal("Update 落库失败应报错")
		}
		if len(applied) != 0 {
			t.Errorf("落库失败不应触发订阅，got %d 次", len(applied))
		}
		if got := s.Current(); got.BalanceCron != base.BalanceCron {
			t.Errorf("Current 应保持旧值 %q，got %q", base.BalanceCron, got.BalanceCron)
		}
	})

	t.Run("合法输入：落库 + Current 更新 + 订阅收到新值", func(t *testing.T) {
		f := &fakeStore{}
		s := NewService(f, base)
		var applied []storage.AppSettings
		s.OnApply(func(v storage.AppSettings) { applied = append(applied, v) })

		good := base
		good.BalanceCron = "0 0 5 * * *"
		good.FailureThreshold = 5
		if err := s.Update(good); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if len(f.saved) != 1 {
			t.Fatalf("应恰落库 1 次，got %d", len(f.saved))
		}
		if got := s.Current(); got.BalanceCron != "0 0 5 * * *" || got.FailureThreshold != 5 {
			t.Errorf("Current 应为新值，got %+v", got)
		}
		if len(applied) != 1 || applied[0].BalanceCron != "0 0 5 * * *" {
			t.Errorf("订阅应收到新值，got %+v", applied)
		}
	})
}

func TestServiceReset(t *testing.T) {
	base := FromConfig(testConfig())
	row := base
	row.BalanceCron = "0 0 5 * * *"

	t.Run("删行失败：保持库值（不半途回退）", func(t *testing.T) {
		f := &fakeStore{row: &row, delErr: errors.New("db down")}
		s := NewService(f, base)
		if err := s.Init(); err != nil {
			t.Fatalf("Init: %v", err)
		}
		var applied []storage.AppSettings
		s.OnApply(func(v storage.AppSettings) { applied = append(applied, v) })

		if err := s.Reset(); err == nil {
			t.Fatal("Reset 删行失败应报错")
		}
		if got := s.Current(); got.BalanceCron != "0 0 5 * * *" {
			t.Errorf("删行失败应保持库值，got %q", got.BalanceCron)
		}
		if len(applied) != 0 {
			t.Errorf("失败的重置不应触发订阅，got %d 次", len(applied))
		}
	})

	t.Run("删行成功：回退 env 值并通知订阅", func(t *testing.T) {
		f := &fakeStore{row: &row}
		s := NewService(f, base)
		if err := s.Init(); err != nil {
			t.Fatalf("Init: %v", err)
		}
		var applied []storage.AppSettings
		s.OnApply(func(v storage.AppSettings) { applied = append(applied, v) })

		if err := s.Reset(); err != nil {
			t.Fatalf("Reset: %v", err)
		}
		if f.deleted != 1 {
			t.Errorf("Delete 调用 %d 次, want 1", f.deleted)
		}
		if got := s.Current(); !reflect.DeepEqual(got, base) {
			t.Errorf("Current 应回退 env 推导值，got %+v", got)
		}
		if len(applied) != 1 || applied[0].BalanceCron != base.BalanceCron {
			t.Errorf("订阅应收到回退后的值，got %+v", applied)
		}
	})
}

// 并发 Update / Current / OnApply 不出竞态（-race 覆盖；
// 对应边界用例「PUT 与扫描并发」）。
func TestServiceConcurrent(t *testing.T) {
	base := FromConfig(testConfig())
	s := NewService(&fakeStore{}, base)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	s.OnApply(func(storage.AppSettings) {})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			v := base
			v.FailureThreshold = i%5 + 1
			_ = s.Update(v)
			_ = s.Current()
		}
	}()
	_ = s.Current()
	<-done
}
