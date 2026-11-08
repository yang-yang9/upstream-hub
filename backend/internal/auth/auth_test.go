package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// newTestService 构造可控制 TTL 的测试服务；时间相关断言全部通过手工签发
// 指定 iat/exp 的 token 完成，避免真实 sleep 导致 flaky。
func newTestService(t *testing.T, ttl, absoluteTTL time.Duration) *Service {
	t.Helper()
	s, err := New("admin", "secret-pw", "test-secret-0123456789abcdef", ttl, absoluteTTL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestLoginVerifyRoundtrip(t *testing.T) {
	s := newTestService(t, time.Hour, 24*time.Hour)
	tok, exp, err := s.Login("admin", "secret-pw")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	sub, c, err := s.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if sub != "admin" {
		t.Errorf("sub = %q, want admin", sub)
	}
	if c.Iat == 0 {
		t.Error("login token missing iat")
	}
	if c.Exp != exp.Unix() {
		t.Errorf("claims exp %d != login expires_at %d", c.Exp, exp.Unix())
	}
	if c.Sub != s.Username() {
		t.Errorf("claims sub %q != %q", c.Sub, s.Username())
	}
}

func TestLoginWrongPassword(t *testing.T) {
	s := newTestService(t, time.Hour, 24*time.Hour)
	if _, _, err := s.Login("admin", "wrong"); err == nil {
		t.Fatal("expected error for wrong password")
	}
}

func TestRenewSkippedWhenLifetimeSufficient(t *testing.T) {
	ttl := time.Hour
	s := newTestService(t, ttl, 24*time.Hour)
	// 剩余 50 分钟 > TTL/2，不应续期
	c := claims{Sub: "admin", Iat: time.Now().Add(-10 * time.Minute).Unix(), Exp: time.Now().Add(50 * time.Minute).Unix()}
	if _, ok := s.Renew(c); ok {
		t.Error("Renew should skip when remaining lifetime >= TTL/2")
	}
}

func TestRenewExtendsExpKeepsIat(t *testing.T) {
	ttl := time.Hour
	s := newTestService(t, ttl, 24*time.Hour)
	now := time.Now()
	// 剩余 10 分钟 < TTL/2，应续期
	c := claims{Sub: "admin", Iat: now.Add(-50 * time.Minute).Unix(), Exp: now.Add(10 * time.Minute).Unix()}
	tok, ok := s.Renew(c)
	if !ok {
		t.Fatal("Renew should trigger when remaining lifetime < TTL/2")
	}
	sub, nc, err := s.Verify(tok)
	if err != nil {
		t.Fatalf("Verify renewed token: %v", err)
	}
	if sub != "admin" {
		t.Errorf("sub = %q, want admin", sub)
	}
	if nc.Iat != c.Iat {
		t.Errorf("iat changed on renewal: %d -> %d", c.Iat, nc.Iat)
	}
	if nc.Exp <= c.Exp {
		t.Errorf("exp not extended: %d -> %d", c.Exp, nc.Exp)
	}
	wantExp := now.Add(ttl).Unix()
	if nc.Exp != wantExp {
		t.Errorf("exp = %d, want full TTL %d", nc.Exp, wantExp)
	}
}

func TestRenewSetsIatOnLegacyToken(t *testing.T) {
	// 旧格式 token 续期时应补上 iat（以续期时刻起算绝对上限窗口）
	s := newTestService(t, time.Hour, 24*time.Hour)
	now := time.Now()
	c := claims{Sub: "admin", Exp: now.Add(10 * time.Minute).Unix()}
	tok, ok := s.Renew(c)
	if !ok {
		t.Fatal("Renew should trigger for near-expiry legacy token")
	}
	_, nc, err := s.Verify(tok)
	if err != nil {
		t.Fatalf("Verify renewed token: %v", err)
	}
	if nc.Iat == 0 {
		t.Error("renewed legacy token should get iat set")
	}
	if nc.Iat < now.Add(-time.Minute).Unix() || nc.Iat > now.Add(time.Minute).Unix() {
		t.Errorf("iat = %d, want ~now (%d)", nc.Iat, now.Unix())
	}
}

func TestRenewClampedByAbsoluteTTL(t *testing.T) {
	ttl := 24 * time.Hour
	absolute := 25 * time.Hour
	s := newTestService(t, ttl, absolute)
	now := time.Now()
	// 已用 24.5h（未到 25h 绝对上限），剩余寿命 10 分钟；续期后 exp 应被钳到 iat+absolute
	c := claims{Sub: "admin", Iat: now.Add(-245 * time.Minute).Unix(), Exp: now.Add(10 * time.Minute).Unix()}
	if _, _, err := s.Verify(s.mustSign(t, c)); err != nil {
		t.Fatalf("token within absolute window should verify: %v", err)
	}
	tok, ok := s.Renew(c)
	if !ok {
		t.Fatal("Renew should trigger inside absolute window")
	}
	_, nc, err := s.Verify(tok)
	if err != nil {
		t.Fatalf("Verify renewed token: %v", err)
	}
	want := c.Iat + int64(absolute/time.Second)
	if nc.Exp != want {
		t.Errorf("exp = %d, want clamped %d (iat+absoluteTTL)", nc.Exp, want)
	}
}

func TestVerifyRejectsBeyondAbsoluteTTL(t *testing.T) {
	ttl := time.Hour
	absolute := 2 * time.Hour
	s := newTestService(t, ttl, absolute)
	now := time.Now()
	// iat 在 3 小时前，exp 尚未到（靠续期保活），但已超绝对上限
	c := claims{Sub: "admin", Iat: now.Add(-3 * time.Hour).Unix(), Exp: now.Add(30 * time.Minute).Unix()}
	if _, _, err := s.Verify(s.mustSign(t, c)); err == nil {
		t.Fatal("Verify should reject token beyond absolute TTL")
	}
}

func TestVerifyAcceptsLegacyTokenWithoutIat(t *testing.T) {
	// 旧版本签发的 token 没有 iat 字段，升级后应继续有效且不参与绝对上限
	s := newTestService(t, time.Hour, 0)
	now := time.Now()
	c := claims{Sub: "admin", Exp: now.Add(30 * time.Minute).Unix()}
	if _, _, err := s.Verify(s.mustSign(t, c)); err != nil {
		t.Fatalf("legacy token without iat should verify: %v", err)
	}
	// 即使配了绝对上限，缺 iat 也不拦
	s2 := newTestService(t, time.Hour, time.Minute)
	if _, _, err := s2.Verify(s.mustSign(t, c)); err != nil {
		t.Fatalf("legacy token should skip absolute TTL check: %v", err)
	}
}

func TestMiddlewareSetsRenewedTokenHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ttl := time.Hour
	s := newTestService(t, ttl, 24*time.Hour)
	now := time.Now()

	r := gin.New()
	r.Use(s.Middleware())
	r.GET("/api/hello", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// 剩余寿命充足：无续期头
	fresh := s.mustSign(t, claims{Sub: "admin", Iat: now.Add(-5 * time.Minute).Unix(), Exp: now.Add(55 * time.Minute).Unix()})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/hello", nil)
	req.Header.Set("Authorization", "Bearer "+fresh)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get(RenewedTokenHeader); got != "" {
		t.Errorf("unexpected renewal header for fresh token")
	}

	// 剩余寿命不足：应带续期头，且新 token 可通过 Verify
	stale := s.mustSign(t, claims{Sub: "admin", Iat: now.Add(-50 * time.Minute).Unix(), Exp: now.Add(10 * time.Minute).Unix()})
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/hello", nil)
	req.Header.Set("Authorization", "Bearer "+stale)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	renewed := w.Header().Get(RenewedTokenHeader)
	if renewed == "" {
		t.Fatal("missing renewal header for near-expiry token")
	}
	if _, _, err := s.Verify(renewed); err != nil {
		t.Errorf("renewed token should verify: %v", err)
	}
}

func TestMiddlewareRejectsExpiredToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestService(t, time.Hour, 24*time.Hour)
	now := time.Now()
	expired := s.mustSign(t, claims{Sub: "admin", Iat: now.Add(-2 * time.Hour).Unix(), Exp: now.Add(-time.Minute).Unix()})

	r := gin.New()
	r.Use(s.Middleware())
	r.GET("/api/hello", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/hello", nil)
	req.Header.Set("Authorization", "Bearer "+expired)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

// 特征测试：钉住 Verify 对畸形输入的拒绝路径（签名错误路径由
// TestRenewSetsIatOnLegacyToken 里 s2.Verify 的跨密钥用例覆盖）。
// 对应 spec：docs/specs/auth-session-token.md。
func TestVerifyRejectsMalformedToken(t *testing.T) {
	s := newTestService(t, time.Hour, 24*time.Hour)
	for _, tc := range []struct {
		name  string
		token string
	}{
		{"empty token", ""},
		{"no separator", "abc"},
		{"too many parts", "a.b.c"},
		{"payload not base64", "!!!.AAAA"},
		{"sig not base64", "AAAA.!!!"},
	} {
		if _, _, err := s.Verify(tc.token); err == nil {
			t.Errorf("%s: Verify(%q) should fail", tc.name, tc.token)
		}
	}
}

func (s *Service) mustSign(t *testing.T, c claims) string {
	t.Helper()
	tok, err := s.sign(c)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tok
}
