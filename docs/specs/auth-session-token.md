# Spec：认证会话与滑动续期

> 管理面板登录态：HMAC 签名 token、中间件校验、剩余寿命不足时无感滑动续期（带绝对上限）。

## 背景

单管理员用户的会话认证。`auth.Service` 签发 token（payload 含 username / iat / exp，HMAC-SHA256 签名，base64 两段式），gin 中间件校验并把续期后的新 token 通过 `X-Renewed-Token` 响应头下发；前端 `apiFetch` 收到该头即无感替换 localStorage 里的 token，401 时清 token 并回到登录页。滑动续期有**绝对上限**（自 iat 起算），防止 token 被无限保活。

## 行为规格

### AC-1: 登录与校验往返

- **Given** 正确的用户名 / 密码
- **When** `Login` 签发 token 后 `Verify`
- **Then** 返回 username 且无错；密码错误返回错；token 结构非法（空 / 无分隔符 / 段数不对 / 非 base64）校验直接报错

### AC-2: 滑动续期触发条件

- **Given** token 剩余寿命 > tokenTTL/2
- **When** `Renew(claims)`
- **Then** 跳过续期（返回 false），token 保持原样

- **Given** 剩余寿命不足 tokenTTL/2
- **When** `Renew`
- **Then** 重签新 token：**iat 保留**（首次签发时间）、exp 尽量推满一个 TTL

### AC-3: 绝对上限封顶

- **Given** absoluteTTL > 0 且 token 距 iat 已超上限
- **When** `Verify` / `Renew`
- **Then** Verify 按过期拒绝（要求重新登录）；续期前即使寿命不足也不续；absoluteTTL ≤ 0（未配置）不封顶

### AC-4: 旧 token 兼容

- **Given** 旧版本签发的 token 无 iat 字段（=0）
- **When** `Verify` / `Renew`
- **Then** Verify 正常通过；Renew 为其补写 iat；绝对上限检查对缺 iat 的 token 跳过

### AC-5: 中间件行为

- **Given** 请求带有效 token 且触发了续期
- **When** 中间件处理
- **Then** 响应带 `X-Renewed-Token` 头（新 token）；token 过期的请求被拒绝

### AC-6: 前端配合

- **Given** 响应带 `X-Renewed-Token`
- **When** `apiFetch` 解析响应
- **Then** 无感替换本地 token；401 且非 `skipAuthErrorHandler`（登录请求本身）时清 token + 触发全局 unauthorizedHandler

## 边界用例

| 边界 | 期望 | 测试 |
|------|------|------|
| token 残缺（空 / "abc" / "a.b.c" / 非 base64 段） | Verify 报错不 panic | `TestVerifyRejectsMalformedToken` |
| 旧 token 无 iat | 兼容通过，Renew 补 iat | `TestVerifyAcceptsLegacyTokenWithoutIat` / `TestRenewSetsIatOnLegacyToken` |
| 恰好等于绝对上限 | 未超（严格大于判定） | `TestVerifyRejectsBeyondAbsoluteTTL` |
| 寿命充足时不续 | Renew 返回 false | `TestRenewSkippedWhenLifetimeSufficient` |
| 登录请求 401 | 前端不触发登出（登录页自己展示错误） | `lib/api.test.ts` |
| 环境变量 | `AUTH_SESSION_TTL_HOURS` 绑定 | 见 config 层 |

## 非目标

- 不做多用户 / RBAC（单管理员 + 只读访客在 roadmap 另立）。
- 不做 refresh token 机制（滑动续期由服务端在同一 token 上完成）。
- 不做 2FA。

## 关联代码

- 实现：`backend/internal/auth/auth.go` — `Login` / `Verify` / `Renew` / `exceededAbsoluteTTL` / `Middleware`
- 单元测试：`backend/internal/auth/auth_test.go` — 全部 Test*（预签 token 驱动，无 HTTP / DB）
- 前端：`frontend/lib/api.ts` — `apiFetch` / `applyRenewedToken` / `setUnauthorizedHandler`；测试 `frontend/lib/api.test.ts`

## 变更记录

- 2026-11-08 — 初稿（SDD 改造回填，AC 对应 Phase 3 与 Phase 7 测试）
