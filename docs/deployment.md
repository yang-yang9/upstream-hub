# 部署与使用手册

upstream-hub 是面向 NewAPI / Sub2API 站点的上游渠道监控面板。本文覆盖部署、配置、升级和各功能模块的使用。

> 面板预览和通知渠道字段示例见仓库根目录 [README.md](../README.md)。本文侧重部署与运行配置。

---

## 一、部署方式

推荐 **Docker Compose**（自带 PostgreSQL）。也可纯二进制 / 源码跑，但需自备数据库。

### 1.1 Docker Compose（推荐）

```bash
git clone https://github.alibaba-inc.com/yang-yang9/upstream-hub.git
cd upstream-hub
cp .env.example .env
```

编辑 `.env`，**至少**设置：

```env
APP_SECRET=请替换为 32 字节以上随机字符串
POSTGRES_PASSWORD=请替换为数据库密码
```

公网暴露时同时开启后台登录：

```env
AUTH_ENABLED=true
ADMIN_USERNAME=admin
ADMIN_PASSWORD=请替换为强密码
```

启动：

```bash
docker compose up -d
```

启动后访问 `http://<宿主机>:8080`。

镜像地址：`ghcr.io/yang-yang9/upstream-hub`。

| Tag | 含义 |
| --- | --- |
| `edge` | 跟随 dev 分支每次提交的构建（默认） |
| `latest` | 最近一次正式发布（打 `v*.*.*` tag 时发布） |
| `v0.2.0` / `0.2` | 锁定到某个具体版本 |

在 `.env` 里用 `UPSTREAMHUB_IMAGE_TAG` 切换：

```env
UPSTREAMHUB_IMAGE_TAG=v0.2.0   # 生产建议锁定具体版本
```

> **GHCR 私有包**：ghcr.io 上的包默认私有。直接 `docker compose pull` 报 401 时，要么在 GitHub 仓库的 Packages 页把包设为 Public，要么在运行机器上 `docker login ghcr.io`（用户名 = GitHub 用户名，密码 = 有 `read:packages` 权限的 PAT）。

### 1.2 架构说明

当前 CI 只构建 **linux/amd64**。Apple Silicon 等 ARM 机器无法直接运行，需在 x86_64 主机或开启 QEMU 的环境部署。

### 1.3 纯二进制 / 源码（非 Docker 部署）

需要 Go 1.23 + Node 20 + 本地 PostgreSQL。

#### Go embed 机制（必读）

upstream-hub 是一个**单体二进制**——前端 React 应用在编译时嵌入到 Go 可执行文件里，运行时由 Go 进程直接 serve，不需要 nginx 转发 /api 以外的路径，也不需要单独起 Node 进程跑前端。

具体流程见 `backend/web/web.go`：

```go
//go:embed all:dist
var dist embed.FS
```

`go build` 时会把 `backend/web/dist/` 下的所有文件打包进二进制。`main.go` 里判断 `web.HasFrontend()` 为 true 时，把这些文件挂到 HTTP 路由上 serve。**如果 `backend/web/dist/` 是空目录或不包含 `index.html`，启动日志会打印 `"no embedded frontend"`，浏览器访问看到的是空白页**。

> ⚠️ 这意味着：**每次改前端代码（`api.ts`、`sync-stream.ts`、任何 `.tsx` 文件等），都必须重新 pnpm build → cp dist 到 embed 目录 → 重新 go build → 重启二进制**。否则线上跑的还是旧前端。
>
> Docker 部署不需要关心这些——`Dockerfile` 里已内置了这两个步骤。

#### 完整部署流程

```bash
# ============================================================
# 1. 拉最新代码
# ============================================================
git pull origin dev          # 跟踪开发分支
# 或切到具体 tag：git checkout v0.2.0

# ============================================================
# 2. 前端构建
# ============================================================
cd frontend
pnpm install                 # 安装依赖（首次或依赖变更后）
pnpm build                   # Vite 构建，产出 dist/
# 构建后产物在 frontend/dist/：
#   dist/index.html          — SPA 入口
#   dist/assets/index-xxx.js — 主 bundle（含 api.ts、sync-stream.ts 等）
#   dist/assets/index-xxx.css
```

**说明**：`pnpm build` 用 Vite 打包 React 应用，输出纯静态 HTML/JS/CSS。产物放在 `frontend/dist/`，与 `backend/web/dist/` 不是同一个目录——还需要下面这一步。

```bash
# ============================================================
# 3. 拷贝前端产物到 Go embed 目录 ★★★★★
# ============================================================
# 这是整个部署流程里最容易漏掉、也最致命的一步。
#
# 上面的 build 只把产物放在 frontend/dist/。
# 但 go build 的 embed 指令读的是 backend/web/dist/。
# 不拷贝这一步，编译出来的二进制就会嵌入旧前端（或根本没有前端）。
#
# 典型症状：
#   • 改了 api.ts 里的 applyRenewedToken() 逻辑 → 部署后仍不生效
#   • 改了某个页面组件 → 部署后 UI 没变化
#   • 启动日志显示 "no embedded frontend" → 浏览器白屏
#   → 根因全是忘了这步。
rm -rf ../backend/web/dist
cp -r dist ../backend/web/dist
# 验证：
ls ../backend/web/dist/index.html   # 必须存在，改过前端的话时间戳应该是新的

# ============================================================
# 4. 后端编译（注入版本号）
# ============================================================
cd ../backend
# build 参数说明：
#   -trimpath  去除编译机器路径前缀（可复现构建）
#   -ldflags   注入版本号 + 剥离调试信息（-s减小体积）
#             版本号影响面板「系统升级」功能——与 GitHub Release tag 比对
go build -trimpath \
  -ldflags="-s -w -X github.com/worryzyy/upstream-hub/internal/version.Version=v0.2.0" \
  -o upstream-hub ./cmd/server

# 验证二进制：
./upstream-hub --help          # 应打印 flag 帮助
file ./upstream-hub            # 应显示 ELF 64-bit
```

**如果只改了后端 Go 代码（没动前端）**：跳过步骤 2 和 3，只需步骤 4 + 重启。

```bash
# ============================================================
# 5. 环境变量 + 启动
# ============================================================
# 推荐方式：写 .env 文件用 systemd 管理（见下方 1.4 节）。
# 快速测试方式：
APP_SECRET=<32字节随机串> \
AUTH_ENABLED=true \
ADMIN_USERNAME=admin \
ADMIN_PASSWORD=<强密码> \
UPSTREAMHUB_DATABASE_HOST=127.0.0.1 \
UPSTREAMHUB_DATABASE_PORT=5432 \
UPSTREAMHUB_DATABASE_USER=upstreamhub \
UPSTREAMHUB_DATABASE_PASSWORD=<数据库密码> \
UPSTREAMHUB_DATABASE_NAME=upstreamhub \
UPSTREAMHUB_SERVER_MODE=release \
./upstream-hub
```

启动后观察日志，正常运行会看到：

```
{"level":"INFO","msg":"starting upstream-hub","version":"v0.2.0","port":8418,...}
{"level":"INFO","msg":"frontend embedded, serving SPA on /"}
{"level":"INFO","msg":"http server listening","addr":":8418"}
```

如果看到 `"no embedded frontend"` 说明步骤 3 忘了执行。

#### 如何验证 token 滑动续期确实生效

滑动续期的完整链路是：后端 auth 中间件在 token 剩余寿命不足一半时重签 → 通过 `X-Renewed-Token` 响应头下发 → 前端 `apiFetch()` / `streamSSE()` 读到后写 localStorage → 后续请求用新 token。任何一环断了续期就不生效。

```bash
# 1. 先确认二进制嵌入了最新前端（步骤 3 真的执行了）
ls -la backend/web/dist/index.html
# 时间戳应该是刚刚 build 的，不是几个月前的。

# 2. 启动后观察服务器日志
grep "token renewed" /path/to/logs
# 正常续期输出示例：
# {"time":"...","level":"INFO","msg":"token renewed","sub":"admin",
#  "iat":1730000000,"old_exp":1730010000,"ttl":"168h0m0s"}
#
# 被绝对上限 clamp 的输出示例：
# {"time":"...","level":"WARN","msg":"token renewal clamped by absolute TTL",
#  "sub":"admin","iat":1730000000,"max_ttl":"720h0m0s"}

# 3. 如果看不到续期日志，对照排查：
```

| 检查项 | 命令 / 方法 | 期望 |
|--------|-----------|------|
| AUTH 是否启用 | `echo $AUTH_ENABLED` | `true` |
| 账号密码是否配置 | `echo $ADMIN_USERNAME $ADMIN_PASSWORD` | 非空 |
| TTL 是否合理 | `echo $AUTH_SESSION_TTL_HOURS` | 非 0（0=永不过期，永不触发续期） |
| 前端是否最新 | `ls -l backend/web/dist/index.html` | 时间戳与 build 时间一致 |
| 后端是否收到请求 | `grep "GET /api" /path/to/logs` | 有请求记录 |
| 续期响应头是否下发 | 浏览器 DevTools → Network → 任选一个 /api/* → Response Headers | 若剩余寿命 < TTL/2，应有 `X-Renewed-Token` |
| 前端是否处理响应头 | DevTools → Application → Local Storage → `uh_token` | 页面开一段时间后 token 值应自动更新 |

#### 本地开发

后端 `go run ./cmd/server`（无嵌入前端，由 vite dev server 接管 :3010），前端 `pnpm dev`。

### 1.4 systemd 服务（推荐的生产环境方式）

非 Docker 部署建议用 systemd 管理进程生命周期：自动启动、crash 重启、日志转储。

```bash
# 1. 创建 systemd 服务文件
sudo tee /etc/systemd/system/upstream-hub.service << 'EOF'
[Unit]
Description=upstream-hub — NewAPI/Sub2API upstream channel monitor
After=network.target postgresql.service
Wants=postgresql.service

[Service]
Type=simple
User=upstream-hub
Group=upstream-hub
WorkingDirectory=/opt/upstream-hub
ExecStart=/opt/upstream-hub/upstream-hub
Restart=always
RestartSec=5
# 安全加固（可选）
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/opt/upstream-hub
PrivateTmp=true

# 环境变量从文件读取
EnvironmentFile=/opt/upstream-hub/.env

# 日志输出到 journald
StandardOutput=journal
StandardError=journal
SyslogIdentifier=upstream-hub

[Install]
WantedBy=multi-user.target
EOF

# 2. 创建应用目录和用户
sudo mkdir -p /opt/upstream-hub
sudo useradd -r -s /bin/false upstream-hub

# 3. 把编译好的二进制放过去
sudo cp backend/upstream-hub /opt/upstream-hub/
sudo chown -R upstream-hub:upstream-hub /opt/upstream-hub

# 4. 创建环境变量文件
sudo tee /opt/upstream-hub/.env << 'ENVEOF'
# 必填
APP_SECRET=<32字节以上随机字符串>
# 数据库
UPSTREAMHUB_DATABASE_HOST=127.0.0.1
UPSTREAMHUB_DATABASE_PORT=5432
UPSTREAMHUB_DATABASE_USER=upstreamhub
UPSTREAMHUB_DATABASE_PASSWORD=<数据库密码>
UPSTREAMHUB_DATABASE_NAME=upstreamhub
# 鉴权
AUTH_ENABLED=true
ADMIN_USERNAME=admin
ADMIN_PASSWORD=<强密码>
AUTH_SESSION_TTL_HOURS=168
AUTH_MAX_SESSION_TTL_HOURS=720
# 运行模式
UPSTREAMHUB_SERVER_MODE=release
UPSTREAMHUB_LOG_LEVEL=info
ENVEOF

sudo chown upstream-hub:upstream-hub /opt/upstream-hub/.env
sudo chmod 600 /opt/upstream-hub/.env   # 含密码，拒绝其他用户读取

# 5. 启用并启动
sudo systemctl daemon-reload
sudo systemctl enable upstream-hub
sudo systemctl start upstream-hub

# 6. 日常运维
sudo systemctl status upstream-hub     # 看状态
sudo journalctl -u upstream-hub -f     # 实时日志
sudo journalctl -u upstream-hub -f | grep "token renewed"  # 看续期日志
sudo systemctl restart upstream-hub    # 更新二进制后重启
```

**更新流程**（发布新版本时）：

```bash
# 在开发机上完成 1.3 的步骤 1-4（构建前端 + 编译后端），
# 然后把二进制 scp 到服务器：
scp backend/upstream-hub user@server:/opt/upstream-hub/upstream-hub.new
ssh user@server '
  chown upstream-hub:upstream-hub /opt/upstream-hub/upstream-hub.new
  mv /opt/upstream-hub/upstream-hub.new /opt/upstream-hub/upstream-hub
  chmod +x /opt/upstream-hub/upstream-hub
  systemctl restart upstream-hub
  echo "--- restarting, tailing logs ---"
  journalctl -u upstream-hub -f -n 20
'
```

也可以用 GitHub Release 的 `upstream-hub-linux-amd64.tar.gz`（由 CI 自动构建），解压后替换二进制即可——CI 构建时已经嵌入前端。注意 CI 二进制用的 `BuildVersion` 是 CI 自动注入的 tag，不影响功能。

---

## 二、环境变量参考

除下表特别标注外，所有变量前缀 `UPSTREAMHUB_`，点号 `_` 替换（如 `server.port` → `UPSTREAMHUB_SERVER_PORT`）。「特殊」表示不带前缀的独立约定变量。

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| **APP_SECRET** *(特殊)* | — | **必填**。AES-GCM 主密钥，加密通知渠道密钥/Webhook/SMTP 密码等敏感字段。至少 32 字节随机串。**修改后既有数据无法解密，务必妥善保存。** |
| **AUTH_ENABLED** *(特殊)* | `false` | 后台鉴权开关。`false` 时所有 `/api/*` 免登录（仅适合内网/反代后）。`true` 时需配 `ADMIN_USERNAME`/`ADMIN_PASSWORD`。 |
| **ADMIN_USERNAME** *(特殊)* | `admin` | 后台登录账号，`AUTH_ENABLED=true` 时生效。 |
| **ADMIN_PASSWORD** *(特殊)* | — | 后台登录密码，`AUTH_ENABLED=true` 时必填。 |
| **AUTH_TOKEN_SECRET** *(特殊)* | 回退用 APP_SECRET | HMAC token 签名密钥，留空时回退使用 `APP_SECRET`。 |
| **AUTH_SESSION_TTL_HOURS** *(特殊)* | `168` | 单次 token 有效期（小时）。剩余寿命 < TTL/2 时自动续期（推后 exp、保留 iat）。 |  
| **AUTH_MAX_SESSION_TTL_HOURS** *(特殊)* | `720` | token 滑动续期的累计有效期上限（小时），自首次登录起算，到点强制重新登录。`0` = 不封顶。 |
| UPSTREAMHUB_SERVER_PORT | `8418` | 后端监听端口（容器内）。 |
| UPSTREAMHUB_SERVER_MODE | `debug` | Gin 模式：`release` / `debug`。生产用 `release`。 |
| UPSTREAMHUB_SERVER_BASEURL | `http://localhost:8418` | 对外 base URL。 |
| UPSTREAMHUB_SERVER_TRUSTEDPROXIES | — | 受信代理 IP 列表，反代后部署时设置。 |
| UPSTREAMHUB_METRICS_TOKEN | — | `/metrics` 端点的 Bearer token。留空 = 公开（内网默认，同 `/healthz`）；设置后抓取需带 `Authorization: Bearer <token>`。Prometheus 侧对应 `bearer_token` 配置。 |
| UPSTREAMHUB_DATABASE_HOST | `localhost` | PostgreSQL 主机。compose 下默认指向 `postgres` 容器。 |
| UPSTREAMHUB_DATABASE_PORT | `54329` | PostgreSQL 端口。compose 容器内为 `5432`。 |
| UPSTREAMHUB_DATABASE_USER | — | 数据库用户。 |
| UPSTREAMHUB_DATABASE_PASSWORD | — | 数据库密码。 |
| UPSTREAMHUB_DATABASE_NAME | — | 数据库名。 |
| UPSTREAMHUB_DATABASE_SSLMODE | `disable` | PG SSL 模式。 |
| UPSTREAMHUB_DATABASE_TIMEZONE | `Asia/Shanghai` | 数据库时区。 |
| UPSTREAMHUB_DATABASE_MAXOPENCONNS | `20` | 连接池上限。 |
| UPSTREAMHUB_DATABASE_MAXIDLECONNS | `5` | 连接池空闲上限。 |
| UPSTREAMHUB_SCHEDULER_BALANCECRON | `37 */15 * * * *` | 余额扫描 cron（6 段含秒）。 |
| UPSTREAMHUB_SCHEDULER_RATECRON | `13 */30 * * * *` | 倍率扫描 cron。 |
| UPSTREAMHUB_SCHEDULER_CONCURRENCY | `4` | 扫描并发数。 |
| UPSTREAMHUB_SCHEDULER_RETENTION_CRON | `0 17 3 * * *` | 历史清理 cron（每天 03:17）。 |
| UPSTREAMHUB_SCHEDULER_RETENTION_MONITORLOGSDAYS | `30` | monitor 日志保留天数，0=不清理。 |
| UPSTREAMHUB_SCHEDULER_RETENTION_BALANCESNAPSHOTSDAYS | `90` | 余额快照保留天数。 |
| UPSTREAMHUB_SCHEDULER_RETENTION_NOTIFICATIONLOGSDAYS | `90` | 通知日志保留天数。 |
| UPSTREAMHUB_NOTIFICATIONS_BATCHRATECHANGES | `true` | 同次扫描多个倍率变化合并成 1 条通知。 |
| UPSTREAMHUB_NOTIFICATIONS_MINCHANGEPCT | `0` | 涨跌幅 < X% 的倍率变化跳过推送。0=全发。 |
| UPSTREAMHUB_NOTIFICATIONS_BALANCELOWCOOLDOWNMINUTES | `60` | 同渠道 `balance_low` 推送冷却分钟。0=不冷却。 |
| UPSTREAMHUB_NOTIFICATIONS_QUIETHOURS | *(空)* | 静默时段 `HH:MM-HH:MM`（服务器本地时区），窗口内全部通知不推送、不占冷却名额。空=关闭（7×24 照发）。跨午夜写法如 `23:00-08:00`；非法格式拒绝启动。 |
| UPSTREAMHUB_NOTIFICATIONS_BALANCERECOVERY | `false` | 余额恢复通知开关。`true` = 低余额告警后余额回升到阈值之上时发"余额已恢复"（`balance_recovered`）。默认关闭；关闭时只维护内部状态列，通知行为不变。 |
| UPSTREAMHUB_NOTIFICATIONS_SENDMAXATTEMPTS | `3` | 通知失败最大尝试次数（含首次），指数退避。 |
| **GITHUB_PROXY** *(特殊)* | — | 升级时访问 GitHub 的代理前缀，国内网络必填，如 `https://ghproxy.com`。详见下文「升级」。 |
| UPSTREAMHUB_UPDATER_GITHUBREPO | `yang-yang9/upstream-hub` | 升级检查的 GitHub 仓库。fork 自用且自行发版时改成自己的 `owner/repo`。 |
| UPSTREAMHUB_LOG_LEVEL | `info` | 日志等级。 |
| UPSTREAMHUB_LOG_FORMAT | `text` | 日志格式：`text` / `json`。 |
| **UPSTREAMHUB_HTTP_PORT** *(compose)* | `8080` | 宿主机映射端口（容器内仍是 8418）。 |
| **UPSTREAMHUB_IMAGE_TAG** *(compose)* | `edge` | compose 拉取的镜像 tag。 |

---

## 三、鉴权与滑动续期

### 3.1 两种部署模式

- **纯内网 / 反代后**：`AUTH_ENABLED=false`（默认），所有 `/api/*` 免登录。前置 nginx/网关做 IP 白名单或 SSO。
- **公网暴露**：必须 `AUTH_ENABLED=true` + 强 `ADMIN_PASSWORD`。

### 3.2 Token 机制

upstream-hub 使用**无状态 HMAC-SHA256 token**：

1. **登录**：`POST /api/auth/login` 验证用户名密码 → 签发 token。token 格式为 `<base64url(JSON payload)>.<base64url(HMAC)>`，payload 含 `sub`（用户名）、`iat`（首次签发时间）、`exp`（过期时间）。
2. **鉴权**：每个 `/api/*` 请求带 `Authorization: Bearer <token>` 头，中间件验签 + 检查过期。
3. **存储**：token 存在浏览器 localStorage（key: `uh_token`），无 HttpOnly cookie——因为 SSE 流式请求也需要鉴权，而同源部署下 XSS 风险可控。

### 3.3 滑动续期（关键）

这是避免"每天登录一次"的核心机制：

```
终端用户                  浏览器                   后端 auth 中间件
  |                       |                          |
  |                       |-- GET /api/channels ---->|
  |                       |   Authorization: Bearer  |
  |                       |   <token>                |
  |                       |                          |-- Verify(token) ✓
  |                       |                          |-- 检查剩余寿命
  |                       |                          |   剩余 < TTL/2 ?
  |                       |                          |      → Renew(): 保留 iat
  |                       |                          |        重签新 token(exp=now+TTL)
  |                       |                          |
  |                       |<-- 200 + 数据 -----------|
  |                       |   X-Renewed-Token: <新>  |  ← 响应头
  |                       |                          |
  |                       | applyRenewedToken(res)    |
  |                       |   → setToken(新)          |
  |                       |   → localStorage 更新     |
  |                       |                          |
  |                       |-- 后续请求用新 token ---->|
```

**触发条件**：token 剩余有效时间 < `AUTH_SESSION_TTL_HOURS` 的一半时，中间件自动重签新 token。例如 TTL=168h（7 天），剩余 < 84h 时就触发续期。

**绝对上限**：无论怎么续期，`iat`（首次登录时间）始终不变。自 `iat` 起累计超过 `AUTH_MAX_SESSION_TTL_HOURS`（默认 720h = 30 天）后，`Verify()` 直接返回 401，强制重新登录。防止 token 被无限保活。

**前端如何接收续期**：`frontend/lib/api.ts` 的 `apiFetch()` 和 `frontend/lib/sync-stream.ts` 的 `streamSSE()` 都会在收到响应后调用 `applyRenewedToken(res)`——读取 `X-Renewed-Token` 响应头并写入 localStorage。`refresh-context.tsx` 每 30 秒轮询一次 `/api/auth/me`，所以只要浏览器页面开着，token 就会持续续期。

### 3.4 排障清单

**token 总是过期、续期不生效**：

| 排查点 | 命令 / 方法 | 期望 |
|--------|-----------|------|
| 二进制是否嵌入了最新前端 | `ls -l backend/web/dist/index.html` | 时间戳是最新的 |
| 后端是否在续期 | `grep "token renewed" /path/to/logs` | 有 INFO 日志 |
| 是否被绝对上限 clamp | `grep "renewal clamped" /path/to/logs` | 若有则已超 30 天，需重新登录 |
| 响应头是否下发 | DevTools → Network → /api/* → Response Headers | 有 `X-Renewed-Token` 头 |
| 前端是否写入了 localStorage | DevTools → Application → Local Storage → `uh_token` | 值会更新 |
| AUTH_SESSION_TTL_HOURS 是否正常 | `echo $AUTH_SESSION_TTL_HOURS` | 非 0（0 = 永不过期 = 不触发续期） |
| SSE 流是否正常建立 | DevTools → Network → 找 EventStream 类型请求 | 200 OK，持续接收数据 |

> **最常见的原因**：改了前端代码，但只跑了 `pnpm build`，忘了 `cp dist ../backend/web/dist` 和 `go build`。嵌入在二进制里的还是旧前端 JS——`applyRenewedToken()` 根本不存在。

### 3.5 反向代理示例（nginx，把 8080 转到 443）：

```nginx
server {
    listen 443 ssl;
    server_name hub.example.com;
    # ssl_certificate ...

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

启用反代后，在 `.env` 设 `UPSTREAMHUB_SERVER_TRUSTEDPROXIES` 为反代 IP，否则 Gin 不信任 `X-Forwarded-*`。

---

## 四、面板内升级与重启

设置页「系统升级」卡片支持一键升级：

1. **检查更新**：后端调 GitHub Releases API 拉最新版本，与当前版本对比（缓存 10 分钟）。
2. **升级**：下载对应平台二进制 tar.gz → SHA256 校验 → 原子替换 `/app/upstream-hub`。
3. **重启**：进程 `os.Exit(0)`，Docker `restart: unless-stopped` 自动拉起新二进制（前端随二进制 embed，一并更新）。

### 国内网络

GitHub 下载走不通时设代理：

```env
GITHUB_PROXY=https://ghproxy.com
```

`https://ghproxy.com` 会前缀到 GitHub API 和 asset 下载 URL。

### 升级前提

面板内升级依赖 **GitHub Release 的二进制资产**（`upstream-hub-linux-amd64.tar.gz` + `checksums.txt`）。这些由 `release.yml` 在打 `v*.*.*` tag 时自动发布。所以：

- 只在打了正式版本 tag 后，面板内升级才可用。
- Docker 镜像升级走 `docker compose pull && up -d`，不走面板内升级（两者更新的是不同载体）。

> fork 自用时，把 `UPSTREAMHUB_UPDATER_GITHUBREPO` 改成你自己的 `owner/repo`，并保证你自己的仓库也走 `release.yml` 发了二进制。

---

## 五、打码平台

用于 NewAPI 登录页开了 Cloudflare Turnstile 的渠道。在「打码平台」页配置：

| 字段 | 说明 |
| --- | --- |
| 名称 | 自取，便于区分。 |
| 类型 | `capsolver` / `2captcha` / `anticaptcha` / `yescaptcha`。 |
| API Key | 打码平台账号的 API key，加密保存。 |
| Endpoint | 可选，自建/代理时覆盖默认 API 地址。 |
| Extra | 可选，JSON，预留扩展。 |
| 启用 | 是否参与调度。 |

在渠道编辑里勾选「Turnstile」并关联某个打码配置，扫描登录时自动调用。

---

## 六、通知渠道

支持 Telegram、Webhook、邮件、企业微信、钉钉、飞书、Bark 七种。各渠道的字段格式见 [README.md](../README.md) 的「通知渠道配置」一节。

去抖策略（`UPSTREAMHUB_NOTIFICATIONS_*`）：

- **BatchRateChanges**：同次扫描多个倍率变化合并成 1 条，避免一次大调价刷屏。
- **MinChangePct**：涨跌幅太小的不推送（仍写日志）。
- **BalanceLowCooldownMinutes**：同渠道余额低告警的冷却期，跨重启生效（存 PostgreSQL）。
- **QuietHours**：静默时段（默认关闭）。窗口内全部通知不推送、也不占冷却名额——冷却窗口从出窗后首次触发起算，不会因为静默吞掉。测试发送不受影响。
- **BalanceRecovery**：余额恢复通知（默认关闭，`UPSTREAMHUB_NOTIFICATIONS_BALANCERECOVERY=true` 开启）。余额低于阈值触发过告警的渠道，余额回升到阈值之上时发一条"余额已恢复"。恢复通知不参与冷却、受静默时段约束。详见 [specs/balance-recovery.md](specs/balance-recovery.md)。
- **SendMaxAttempts**：失败指数退避重试。

冷却还可以在渠道表单里按渠道覆盖（`balance_low` / `balance_forecast_low` / `login_failed` / `monitor_failed` 四类事件各配独立分钟数，0 = 不冷却，留空跟随全局），详见 [specs/quiet-hours-cooldown.md](specs/quiet-hours-cooldown.md)。

订阅规则可限制某通知渠道只接收指定上游或指定倍率分组的事件，详见 README。

---

## 七、数据持久化与备份

- PostgreSQL 数据在 `postgres-data` volume（compose）。备份：`docker exec upstreamhub-postgres pg_dump -U upstreamhub upstreamhub > backup.sql`。
- `APP_SECRET` 是数据加密根密钥——**备份密钥和数据库要一起保存**，缺任一都无法解密通知渠道的密钥字段。
- 历史数据清理由 `scheduler.retention` 控制，`rate_change_logs`（倍率变化记录）是业务核心数据，永久保留不清理。

---

## 八、健康检查与排障

- **健康检查**：`GET /healthz`，返回 `{"status":"ok"}`。compose 用它做容器健康探针。
- **版本信息**：`GET /api/version`，返回 `{"version":"v0.2.0"}`。
- **Docker 日志**：`docker compose logs -f app`。
- **systemd 日志**：`sudo journalctl -u upstream-hub -f`。
- **调高日志等级**：`UPSTREAMHUB_LOG_LEVEL=debug` 看请求细节。
- **渠道同步失败**：看 `monitor_logs` 表或面板「监控日志」，`error_message` 字段有具体原因（登录失败 / Turnstile / 上游超时等）。
- **升级失败**：Docker 看 `docker compose logs app`；systemd 看 `journalctl -u upstream-hub`，留意下载/校验/替换日志。

### 8.1 常见排障场景

**启动后访问页面白屏 / 404**：

```bash
# 确认二进制嵌入了前端
./upstream-hub 2>&1 | head -5
# 应看到 "frontend embedded, serving SPA on /"
# 如果是 "no embedded frontend" → 忘了 cp frontend/dist → backend/web/dist，重新编译。
```

**token 续期不生效（本章最常见的问题）**：

1. 确认二进制嵌入了最新前端（`ls -l backend/web/dist/index.html`）。
2. `grep "token renewed"` 看服务端日志——有日志说明后端续期正常，问题在前端。
3. 无日志 → 检查 `AUTH_ENABLED`、`AUTH_SESSION_TTL_HOURS` 配置。
4. DevTools → Network → Response Headers 看是否有 `X-Renewed-Token`。
5. 详见第三章「鉴权与滑动续期」排障清单。

**改了前端代码但部署后没变化**：

```
1. pnpm build          ← 你做了
2. cp dist ../backend/web/dist  ← 你漏了
3. go build            ← 你做了，但嵌入的还是旧 dist
```

**改了环境变量不生效**：

- systemd：`sudo systemctl restart upstream-hub`（改 `.env` 后必须重启）。
- Docker：`docker compose restart app`。
- 解释：环境变量和大部分配置项只在启动时读取一次。

---

## 九、常见问题

**Q：`docker compose pull` 报 401？**
A：ghcr 包是私有的。设为 Public（GitHub Packages → 包设置），或 `docker login ghcr.io` 用 PAT。

**Q：升级按钮点了没反应/报网络错？**
A：国内网络拉不动 GitHub。设 `GITHUB_PROXY=https://ghproxy.com`，并确认已打过 `v*.*.*` tag（release.yml 才会发二进制）。

**Q：arm64 机器拉不到镜像？**
A：当前只发 amd64。在 x86_64 主机部署，或等后续加原生 arm64 runner。

**Q：改了 `APP_SECRET` 后通知渠道全失效？**
A：`APP_SECRET` 是加密根密钥，改了就解不开旧数据。**不要改**，或在改之前重建所有通知渠道配置。

**Q：改 cron / 数据库 / 加密密钥后不生效？**
A：这些只在启动时读取。Docker：`docker compose restart app`；systemd：`sudo systemctl restart upstream-hub`。

**Q：非 Docker 部署，改了前端代码但 token 续期还是不生效？**
A：99% 是忘了把 `frontend/dist/` 拷贝到 `backend/web/dist/` 再编译。完整步骤：`pnpm build` → `rm -rf backend/web/dist && cp -r frontend/dist backend/web/dist` → `go build` → 重启二进制。详见 1.3 节「Go embed 机制」。

**Q：非 Docker 部署，启动后页面白屏？**
A：启动日志如果显示 `"no embedded frontend"`，说明二进制没嵌入前端。重新执行 1.3 节的步骤 3（拷贝 dist）和步骤 4（编译）。

**Q：systemd 里怎么配环境变量？**
A：在 `EnvironmentFile=/opt/upstream-hub/.env` 里写 `KEY=VALUE` 格式（不要 export 前缀），参考 1.4 节的配置模板。
