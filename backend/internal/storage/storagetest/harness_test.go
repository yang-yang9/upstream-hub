package storagetest

import (
	"strings"
	"testing"
)

// bogusDSN 指向必然拒绝连接的端口：withPackageDatabase 的管理库连接是尽力而为，
// 失败被容忍，测试只验证 dbname 的改名结果，不依赖真 PostgreSQL。
const bogusDSN = "host=127.0.0.1 port=1 user=uh password=uh dbname=uh_test sslmode=disable"

// 复刻生产调用形状（测试 → Open/Channels 等包装 → open → withPackageDatabase，
// 两层间接、open 传 callerSkip+1=3），保证 callerSkip 语义与真实路径一致。
func wrapperShim(t *testing.T) string { return openShim(t) }

func openShim(t *testing.T) string { return withPackageDatabase(t, bogusDSN, 3) }

func TestWithPackageDatabaseResolvesCallerPackageDirectory(t *testing.T) {
	got := wrapperShim(t)
	// 本测试在 storagetest 包内，库名后缀应是包目录名 storagetest；
	// 而不是 harness 自身的文件名 / 目录（那是所有包共库、CI 并行踩踏的根源）。
	if !strings.Contains(got, "dbname=uh_test_storagetest") {
		t.Fatalf("DSN 应按测试调用方所在包目录改名为 dbname=uh_test_storagetest，实际: %s", got)
	}
}
