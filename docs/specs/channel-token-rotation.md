# Spec：Sub2API Token 轮换持久化

> Sub2API 渠道使用用户提供的 access/refresh token 对；access token 临期或失效时自动用 refresh token 轮换并把新凭据加密回写。

## 背景

Sub2API 的凭据模式是 token（非用户名密码登录）。access token 是 JWT 但**到期时间是建议性的**（opaque token 以端点校验为准），refresh token 一次性使用。监控扫描每次都要可用会话，因此 `channel.Service.EnsureSession` 对 Sub2API token 渠道走专门分支：进程内互斥（防并发扫描复用同一个一次性 refresh token）→ 重读最新渠道行（防并发修改后用陈旧凭据）→ 校验 / 按需轮换 → 加密持久化。

## 行为规格

### AC-1: 有效 token 直接复用

- **Given** Sub2API token 渠道，access token 经 `CheckAuth` 有效且（配了 refresh token 时）JWT 距过期 > 5 分钟
- **When** `EnsureSession`
- **Then** 不轮换、不写库，返回会话，清空 last_error

### AC-2: 临期主动轮换

- **Given** 配了 refresh token 且 JWT 5 分钟内到期（`jwtExpiresSoon`）
- **When** `EnsureSession`
- **Then** 直接走 `rotateSub2APIToken`（不等 CheckAuth 失败）

### AC-3: 失效被动轮换

- **Given** CheckAuth 返回 401 且配了 refresh token
- **When** `EnsureSession`
- **Then** 走轮换；未配 refresh token 的 401 → 报错"token 已失效且未配置 refresh token，请重新粘贴凭据"并写 last_error

### AC-4: 轮换持久化与密文

- **Given** 轮换成功（上游签发新 access/refresh token）
- **When** 回写渠道
- **Then** 新凭据 JSON 以密文存入 `password_cipher`（`crypto.Cipher` 加密），后续扫描能解密读出**新** token

### AC-5: 陈旧凭据拒绝

- **Given** 渠道凭据模式 / 类型在锁内被并发修改
- **When** 锁内重读渠道行
- **Then** 检测到不符直接报错（credential mode changed / channel type changed），不用旧凭据轮换

### AC-6: 并发互斥

- **Given** 同进程多个扫描并发进入同一渠道的 token 会话保障
- **When** 抢进程内 mutex
- **Then** 串行执行，一次性 refresh token 不会被两个请求复用

## 边界用例

| 边界 | 期望 | 测试 |
|------|------|------|
| refresh token 空 + JWT 临期 | 不轮换，走 CheckAuth | `ensureSub2APITokenSession` 分支 |
| 401 且无 refresh token | 明确报错文案 + last_error | 同上 |
| 解密失败（密钥变更） | 报错不落库 | `readSub2APICredential` |
| 非 401 的 CheckAuth 失败 | 普通报错，不触发轮换 | 同上 |
| 持久化幂等 | 测试内多次扫描轮换计数精确 | `TestSub2APIRefreshPersistence` |

## 非目标

- 不做 NewAPI 渠道的 token 轮换（其 token 长期有效，失效提示用户重贴）。
- 不做跨进程的分布式锁（单实例部署假设；多实例需外部锁，见 roadmap）。
- 不做轮换失败的退避重试（失败写 last_error，等下个周期）。
- 代理（per-channel proxy）配置在轮换时同步更新 `resolved.Proxy`，但其自身行为不在本 spec。

## 关联代码

- 实现：`backend/internal/channel/service.go` — `EnsureSession` / `ensureSub2APITokenSession` / `rotateSub2APIToken` / `readSub2APICredential` / `jwtExpiresSoon` / `persistSession`
- 集成测试（env 门控 `UPSTREAMHUB_TEST_DB=1`）：`backend/internal/channel/refresh_test.go` — `TestSub2APIRefreshPersistence`（httptest 伪造 Sub2API + 真 PostgreSQL 断言密文回写 / 轮换计数 / 陈旧凭据拒绝）

## 变更记录

- 2026-11-08 — 初稿（SDD 改造回填）
