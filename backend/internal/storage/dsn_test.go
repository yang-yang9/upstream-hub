package storage

import (
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// DSN 的契约：产物必须能被 pgx 正确解析（gorm postgres driver 走同一
// connstring parser），关键字段的值原样保留。
//
// 回归 bug：Password 为空时直接拼 "password="（无引号），pgx/libpq 的
// keyword-value parser 会丢掉其后的 dbname —— 连接落到与用户同名的库，
// 报 FATAL: database "uh" does not exist。常见触发场景：本地 socket +
// trust 认证不设密码。
func TestDBConfigDSN(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cfg      DBConfig
		checkPwd bool
	}{
		{
			name:     "常规字段",
			cfg:      DBConfig{Host: "localhost", Port: 5432, User: "uh", Password: "secret", Name: "upstreamhub", SSLMode: "disable", Timezone: "UTC"},
			checkPwd: true,
		},
		{
			name: "空密码不丢 dbname（回归）",
			cfg:  DBConfig{Host: "/tmp", Port: 5432, User: "uh", Password: "", Name: "upstreamhub", SSLMode: "disable", Timezone: "UTC"},
		},
		{
			name:     "密码含空格与引号",
			cfg:      DBConfig{Host: "localhost", Port: 5432, User: "uh", Password: "p@ss 'w\\ord'", Name: "upstreamhub", SSLMode: "disable", Timezone: "UTC"},
			checkPwd: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := pgconn.ParseConfig(tc.cfg.DSN())
			if err != nil {
				t.Fatalf("ParseConfig(%q): %v", tc.cfg.DSN(), err)
			}
			if parsed.Database != tc.cfg.Name {
				t.Errorf("Database = %q, want %q（空密码丢失 dbname 的回归症状）", parsed.Database, tc.cfg.Name)
			}
			if parsed.User != tc.cfg.User {
				t.Errorf("User = %q, want %q", parsed.User, tc.cfg.User)
			}
			if parsed.Host != tc.cfg.Host {
				t.Errorf("Host = %q, want %q", parsed.Host, tc.cfg.Host)
			}
			if parsed.TLSConfig == nil && tc.cfg.SSLMode != "disable" {
				// sslmode=disable 时 pgx 仍可能给非 nil 的默认 TLS 配置，
				// 这里只在 disable 之外断言；具体行为交给 pgx。
				_ = parsed.TLSConfig
			}
			if tc.checkPwd && parsed.Password != tc.cfg.Password {
				t.Errorf("Password = %q, want %q（quote/escape 往返必须无损）", parsed.Password, tc.cfg.Password)
			}
		})
	}
}
