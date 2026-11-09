package storage_test

import (
	"reflect"
	"testing"

	"github.com/worryzyy/upstream-hub/internal/storage"
	"github.com/worryzyy/upstream-hub/internal/storage/storagetest"
)

// TDD red：AppSettings 模型 / NewAppSettings 仓储尚不存在
// （将在 storage/model.go 与 storage/appsettings.go 实现）。
// 对应 spec：docs/specs/runtime-settings.md AC-1（单行表 + 优先级）。
// env 门控（UPSTREAMHUB_TEST_DB=1）：单行 upsert 语义依赖真 PostgreSQL。

func fullAppSettings() *storage.AppSettings {
	return &storage.AppSettings{
		BalanceCron:                   "37 */15 * * * *",
		RateCron:                      "13 */30 * * * *",
		RetentionCron:                 "0 17 3 * * *",
		RetentionMonitorLogsDays:      30,
		RetentionBalanceSnapshotsDays: 90,
		RetentionNotificationLogsDays: 90,
		BatchRateChanges:              true,
		MinChangePct:                  0.5,
		BalanceLowCooldownMinutes:     60,
		FailureThreshold:              3,
		FailureCooldownMinutes:        45,
		QuietHours:                    "23:00-07:00",
		SendMaxAttempts:               3,
		BalanceRecovery:               true,
		ForecastWindowDays:            7,
		ForecastAlertDays:             3,
		ForecastCooldownMinutes:       720,
	}
}

func TestAppSettingsRoundTrip(t *testing.T) {
	db := storagetest.Open(t)
	repo := storage.NewAppSettings(db)

	// 无行 = (nil, nil)：调用方据此回退 env 推导值（优先级语义的起点）。
	got, err := repo.Get()
	if err != nil {
		t.Fatalf("Get 空表: %v", err)
	}
	if got != nil {
		t.Fatalf("空表 Get = %+v, want nil", got)
	}

	row := fullAppSettings()
	if err := repo.Save(row); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err = repo.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Save 后 Get 应返回行")
	}
	want := *row
	want.UpdatedAt = got.UpdatedAt // 由库维护
	if !reflect.DeepEqual(got, &want) {
		t.Errorf("17 字段往返不一致：\n got  %+v\n want %+v", got, &want)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("updated_at 应由库写入")
	}

	// 覆盖保存：单行语义（不产生第二行）。
	row.FailureThreshold = 5
	row.QuietHours = ""
	if err := repo.Save(row); err != nil {
		t.Fatalf("覆盖 Save: %v", err)
	}
	got, err = repo.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.FailureThreshold != 5 || got.QuietHours != "" {
		t.Errorf("覆盖保存未生效：FailureThreshold=%d QuietHours=%q", got.FailureThreshold, got.QuietHours)
	}

	// 删行：回到"从未保存过"。
	if err := repo.Delete(); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, err = repo.Get()
	if err != nil {
		t.Fatalf("Delete 后 Get: %v", err)
	}
	if got != nil {
		t.Errorf("Delete 后 Get = %+v, want nil", got)
	}
}
