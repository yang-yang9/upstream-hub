// Package storagetest 提供真 PostgreSQL 集成测试 harness。
//
// # 门控
//
// 仅当 UPSTREAMHUB_TEST_DB=1 时启用；未设置时 Open 直接 t.Skip，
// 保证 `go test ./...` 在无数据库的环境（本地 / 无 Docker）保持绿。
//
// # DSN 解析顺序
//
//  1. UPSTREAMHUB_TEST_DATABASE_DSN（推荐显式指定）
//  2. config.Load("") 的 database 段（即 UPSTREAMHUB_DATABASE_* 系列环境变量）
//
// # 隔离
//
// harness 按调用方包自动创建独立数据库 <dbname>_<包目录名>（如 uh_test_channel）：
//   - `go test ./...` 并行跑多个包的测试二进制，共享同一个库会互相 TRUNCATE 踩踏；
//     每包一个库即可并行安全（包内测试串行执行）
//   - 专库专建也保证 fallback DSN 指向的原始库的表永远不会被测试读写
//
// 每个测试结束（t.Cleanup）TRUNCATE ... RESTART IDENTITY CASCADE 清空全部表。
// 包内需要中途清表时用 ResetTables。
//
// 详见仓库 docs/development.md 第三节。
package storagetest

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/worryzyy/upstream-hub/internal/config"
	"github.com/worryzyy/upstream-hub/internal/storage"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var (
	migrateMu  sync.Mutex
	migratedDSN = map[string]bool{}
)

// allTables 与 storage.AutoMigrate 的模型列表一一对应。
var allTables = []string{
	"channels",
	"auth_sessions",
	"captcha_configs",
	"rate_snapshots",
	"rate_change_logs",
	"balance_snapshots",
	"notification_channels",
	"notification_logs",
	"notification_cooldowns",
	"monitor_logs",
	"failure_streaks",
}

// Open 打开调用方包专属的测试数据库（未设 UPSTREAMHUB_TEST_DB=1 时 skip）。
// 返回连接池化的 *gorm.DB；表结构在首次打开时 AutoMigrate，测试结束自动清空并关闭。
func Open(t *testing.T) *gorm.DB {
	t.Helper()
	return open(t, 2)
}

// ResetTables 清空指定表（RESTART IDENTITY CASCADE），供包内测试中途复位。
func ResetTables(t *testing.T, db *gorm.DB, tables ...string) {
	t.Helper()
	if err := db.Exec(fmt.Sprintf("TRUNCATE %s RESTART IDENTITY CASCADE", strings.Join(tables, ", "))).Error; err != nil {
		t.Fatalf("reset tables %v: %v", tables, err)
	}
}

// 以下快捷方法等价于 Open + 对应仓储构造。

func Channels(t *testing.T) *storage.Channels {
	t.Helper()
	return storage.NewChannels(open(t, 2))
}

func Rates(t *testing.T) *storage.Rates {
	t.Helper()
	return storage.NewRates(open(t, 2))
}

func Notifications(t *testing.T) *storage.Notifications {
	t.Helper()
	return storage.NewNotifications(open(t, 2))
}

func MonitorLogs(t *testing.T) *storage.MonitorLogs {
	t.Helper()
	return storage.NewMonitorLogs(open(t, 2))
}

func open(t *testing.T, callerSkip int) *gorm.DB {
	t.Helper()
	if os.Getenv("UPSTREAMHUB_TEST_DB") != "1" {
		t.Skip("set UPSTREAMHUB_TEST_DB=1 (see docs/development.md) to run PostgreSQL integration tests")
	}
	dsn := os.Getenv("UPSTREAMHUB_TEST_DATABASE_DSN")
	if dsn == "" {
		cfg, err := config.Load("")
		if err != nil {
			t.Fatalf("resolve test DSN via config: %v", err)
		}
		dsn = cfg.Database.ToStorageConfig().DSN()
	}
	dsn = withPackageDatabase(t, dsn, callerSkip+1)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(8)
	sqlDB.SetMaxIdleConns(8)

	migrateMu.Lock()
	if !migratedDSN[dsn] {
		if err := storage.AutoMigrate(db); err != nil {
			migrateMu.Unlock()
			t.Fatalf("auto migrate: %v", err)
		}
		migratedDSN[dsn] = true
	}
	migrateMu.Unlock()

	t.Cleanup(func() {
		ResetTables(t, db, allTables...)
		sqlDB.Close()
	})
	return db
}

var dbnameRe = regexp.MustCompile(`dbname=[^\s]+`)

// withPackageDatabase 把 DSN 的 dbname 替换成 <dbname>_<调用方包目录名>，
// 数据库不存在时先连 dbname=postgres 创建。失败时 t.Fatal。
// callerSkip 与 open 的语义一致：open 传 callerSkip+1，指向 open 的调用者的调用者。
func withPackageDatabase(t *testing.T, dsn string, callerSkip int) string {
	t.Helper()
	m := dbnameRe.FindString(dsn)
	if m == "" {
		t.Fatal("test DSN has no dbname= component")
	}
	base := strings.TrimPrefix(m, "dbname=")
	name := base + "_" + callerPackage(callerSkip)

	// 尽力创建；连不上管理库或无权限时，只要目标库本身可连也接受。
	if admin, err := gorm.Open(postgres.Open(dbnameRe.ReplaceAllString(dsn, "dbname=postgres")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	}); err == nil {
		if err := admin.Exec(fmt.Sprintf("CREATE DATABASE %q", name)).Error; err != nil &&
			!strings.Contains(err.Error(), "already exists") {
			t.Fatalf("create test database %s: %v", name, err)
		}
		sqlDB, _ := admin.DB()
		sqlDB.Close()
	}
	return dbnameRe.ReplaceAllString(dsn, "dbname="+name)
}

// callerPackage 返回调用链上第 skip 层（open 的调用者的调用者）所在文件所在的
// 目录名（即包目录，如 "api"、"storage"），用作数据库后缀。取不到时退化为 "pkg"。
// 注意 runtime.Caller(0) 是 callerPackage 自身这一帧，因此内部要 +1 跳过；
// 之前的实现漏了这一帧，导致所有包都解析到 harness 自己，共用一个库，
// `go test ./...` 并行跑包时互相 TRUNCATE 踩踏。
func callerPackage(skip int) string {
	_, file, _, ok := runtime.Caller(skip + 1)
	if !ok {
		return "pkg"
	}
	for i := len(file) - 1; i >= 0; i-- {
		if file[i] == '/' {
			dir := file[:i]
			if j := strings.LastIndexByte(dir, '/'); j >= 0 {
				return sanitizeIdent(dir[j+1:])
			}
			return sanitizeIdent(dir)
		}
	}
	return "pkg"
}

var identRe = regexp.MustCompile(`[^a-z0-9_]`)

func sanitizeIdent(s string) string {
	s = strings.ToLower(s)
	s = identRe.ReplaceAllString(s, "_")
	if s == "" {
		return "pkg"
	}
	return s
}
