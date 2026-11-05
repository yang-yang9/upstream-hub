// Package auth 提供单管理员登录：账号密码写在 config 里，登录后下发 HMAC-SHA256 签名的短 token。
//
// Token 格式："<base64url(payload)>.<base64url(hmac)>"，payload 是 {"sub":"<user>","iat":<unix>,"exp":<unix>}。
// 服务端无状态，AppSecret 不变的情况下重启 token 仍有效。
//
// 滑动续期：iat 记录首次签发时间，中间件在每个已鉴权请求上检测剩余寿命不足 TTL/2 时
// 重签 token（保留 iat、推后 exp），经响应头 X-Renewed-Token 交回前端。活跃使用即续期；
// iat 起算的绝对上限（absoluteTTL）封顶，防止 token 被无限保活。
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Service 单管理员登录服务。
type Service struct {
	username    string
	password    string
	secret      []byte
	tokenTTL    time.Duration
	absoluteTTL time.Duration // 滑动续期累计上限（自 iat 起算）；<= 0 表示不封顶
	logger      *slog.Logger  // nil 时回退 slog.Default()
}

// New 构造 Service。secret 推荐 32 字节以上；若为空报错。
// 调用方应在 secret 为空时回退到 APP_SECRET。
// absoluteTTL 是 token 累计有效期上限；<= 0 表示不封顶。
func New(username, password, secret string, ttl, absoluteTTL time.Duration) (*Service, error) {
	if username == "" || password == "" {
		return nil, errors.New("auth username / password are required")
	}
	if secret == "" {
		return nil, errors.New("auth token secret is empty")
	}
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	return &Service{
		username:    username,
		password:    password,
		secret:      []byte(secret),
		tokenTTL:    ttl,
		absoluteTTL: absoluteTTL,
	}, nil
}

func (s *Service) log() *slog.Logger {
	if s.logger != nil {
		return s.logger
	}
	return slog.Default()
}

// SetLogger 注入日志器，方便对接外部 slog.Logger。
func (s *Service) SetLogger(l *slog.Logger) { s.logger = l }

// claims 是签发到 token payload 里的最小必要字段。
// Iat 记录首次签发时间，续期时保留；旧 token 缺 iat（=0）时跳过绝对上限检查。
type claims struct {
	Sub string `json:"sub"`
	Iat int64  `json:"iat,omitempty"`
	Exp int64  `json:"exp"`
}

// Login 校验账号密码，返回新的 token 与过期时间。
func (s *Service) Login(username, password string) (string, time.Time, error) {
	if subtle.ConstantTimeCompare([]byte(username), []byte(s.username)) != 1 ||
		subtle.ConstantTimeCompare([]byte(password), []byte(s.password)) != 1 {
		return "", time.Time{}, errors.New("invalid username or password")
	}
	now := time.Now()
	expiresAt := now.Add(s.tokenTTL)
	c := claims{Sub: s.username, Iat: now.Unix(), Exp: expiresAt.Unix()}
	tok, err := s.sign(c)
	if err != nil {
		return "", time.Time{}, err
	}
	return tok, expiresAt, nil
}

// Verify 校验 token 合法性并返回 subject 与完整 claims。
// 超过绝对上限（自 iat 起算）时视为过期，要求重新登录。
func (s *Service) Verify(token string) (string, claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return "", claims{}, errors.New("malformed token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", claims{}, fmt.Errorf("decode payload: %w", err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", claims{}, fmt.Errorf("decode sig: %w", err)
	}
	expectedSig := s.mac(payload)
	if subtle.ConstantTimeCompare(sig, expectedSig) != 1 {
		return "", claims{}, errors.New("bad signature")
	}
	var c claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return "", claims{}, fmt.Errorf("decode claims: %w", err)
	}
	now := time.Now()
	if now.Unix() > c.Exp {
		return "", claims{}, errors.New("token expired")
	}
	if s.exceededAbsoluteTTL(c, now) {
		return "", claims{}, errors.New("token exceeded max session lifetime, login again")
	}
	if c.Sub != s.username {
		return "", claims{}, errors.New("unknown subject")
	}
	return c.Sub, c, nil
}

// exceededAbsoluteTTL 判断 token 是否已超过自 iat 起算的累计有效期。
// absoluteTTL <= 0（未配置）或旧 token 缺 iat 时视为未超。
func (s *Service) exceededAbsoluteTTL(c claims, now time.Time) bool {
	if s.absoluteTTL <= 0 || c.Iat <= 0 {
		return false
	}
	return now.Unix() > c.Iat+int64(s.absoluteTTL/time.Second)
}

// Renew 滑动续期：剩余寿命不足 tokenTTL 一半且未触绝对上限时，
// 保留 iat 重签新 token（exp 尽量推满一个 TTL，但不超过 iat+absoluteTTL）。
// 返回新 token 与是否续期；不满足续期条件时 token 原样继续有效。
func (s *Service) Renew(c claims) (string, bool) {
	now := time.Now()
	// 剩余寿命还多：不值得续，避免每个请求都重签
	if time.Duration(c.Exp-now.Unix())*time.Second >= s.tokenTTL/2 {
		return "", false
	}
	// 绝对上限之外没有可续的空间（Verify 已经拦了超上限的情况，这里兜底）
	if s.exceededAbsoluteTTL(c, now) {
		return "", false
	}
	expiresAt := now.Add(s.tokenTTL)
	iat := c.Iat
	if iat == 0 {
		// 旧格式 token 没有 iat：以本次续期时间起算绝对上限
		iat = now.Unix()
	} else if s.absoluteTTL > 0 {
		if maxExp := iat + int64(s.absoluteTTL/time.Second); expiresAt.Unix() > maxExp {
			expiresAt = time.Unix(maxExp, 0)
			s.log().Warn("token renewal clamped by absolute TTL",
				"sub", c.Sub,
				"iat", iat,
				"max_ttl", s.absoluteTTL.String(),
			)
		}
		if !expiresAt.After(now) {
			return "", false
		}
	}
	renewed, err := s.sign(claims{Sub: c.Sub, Iat: iat, Exp: expiresAt.Unix()})
	if err != nil {
		return "", false
	}
	return renewed, true
}

func (s *Service) sign(c claims) (string, error) {
	body, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	sig := s.mac(body)
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func (s *Service) mac(payload []byte) []byte {
	m := hmac.New(sha256.New, s.secret)
	m.Write(payload)
	return m.Sum(nil)
}

// Username 返回当前 Service 绑定的管理员账号（前端展示用）。
func (s *Service) Username() string { return s.username }

// TokenTTL 返回登录 token 的有效期。
func (s *Service) TokenTTL() time.Duration { return s.tokenTTL }

// Middleware 校验 Authorization 头。不通过返回 401。
//
// 滑动续期：校验通过后如果 token 剩余寿命不足 TTL/2，重签新 token 经
// X-Renewed-Token 响应头返回给前端；前端读到即无感替换本地 token。
//
// 路径白名单（不需要鉴权）：
//   - "/healthz"
//   - "/api/version"
//   - "/api/auth/login"
func (s *Service) Middleware() gin.HandlerFunc {
	whitelist := map[string]struct{}{
		"/healthz":        {},
		"/api/version":    {},
		"/api/auth/login": {},
	}
	return func(c *gin.Context) {
		if _, ok := whitelist[c.FullPath()]; ok {
			c.Next()
			return
		}
		if _, ok := whitelist[c.Request.URL.Path]; ok {
			c.Next()
			return
		}
		token := extractToken(c.Request)
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			return
		}
		sub, cl, err := s.Verify(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}
		if renewed, ok := s.Renew(cl); ok {
			// 头要在 handler 写响应前设置，流式（SSE）响应同样带上
			c.Header(RenewedTokenHeader, renewed)
			s.log().Info("token renewed",
				"sub", sub,
				"iat", cl.Iat,
				"old_exp", cl.Exp,
				"ttl", s.tokenTTL.String(),
			)
		}
		c.Set("authSubject", sub)
		c.Next()
	}
}

// RenewedTokenHeader 滑动续期新 token 的响应头名，前端据此无感更新本地 token。
const RenewedTokenHeader = "X-Renewed-Token"

func extractToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	if t := r.URL.Query().Get("token"); t != "" {
		return t
	}
	if c, err := r.Cookie("uh_token"); err == nil {
		return c.Value
	}
	return ""
}
