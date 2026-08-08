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

### 1.3 纯二进制 / 源码

需要 Go 1.23 + Node 20 + 本地 PostgreSQL。

```bash
# 前端
cd frontend && pnpm install && pnpm build
cd ..

# 把前端产物放到 embed 位置
rm -rf backend/web/dist && cp -r frontend/dist backend/web/dist

# 后端（注入版本号）
cd backend
go build -trimpath \
  -ldflags="-s -w -X github.com/worryzyy/upstream-hub/internal/version.Version=v0.2.0" \
  -o upstream-hub ./cmd/server

# 配置走环境变量或 config.yaml
APP_SECRET=<32字节随机串> \
UPSTREAMHUB_DATABASE_HOST=127.0.0.1 \
UPSTREAMHUB_DATABASE_PORT=5432 \
UPSTREAMHUB_DATABASE_USER=upstreamhub \
UPSTREAMHUB_DATABASE_PASSWORD=xxx \
UPSTREAMHUB_DATABASE_NAME=upstreamhub \
./upstream-hub
```

本地开发：后端 `go run ./cmd/server`（无嵌入前端，由 vite dev server 接管 :3010），前端 `pnpm dev`。

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
| UPSTREAMHUB_SERVER_PORT | `8418` | 后端监听端口（容器内）。 |
| UPSTREAMHUB_SERVER_MODE | `debug` | Gin 模式：`release` / `debug`。生产用 `release`。 |
| UPSTREAMHUB_SERVER_BASEURL | `http://localhost:8418` | 对外 base URL。 |
| UPSTREAMHUB_SERVER_TRUSTEDPROXIES | — | 受信代理 IP 列表，反代后部署时设置。 |
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
| UPSTREAMHUB_NOTIFICATIONS_SENDMAXATTEMPTS | `3` | 通知失败最大尝试次数（含首次），指数退避。 |
| **GITHUB_PROXY** *(特殊)* | — | 升级时访问 GitHub 的代理前缀，国内网络必填，如 `https://ghproxy.com`。详见下文「升级」。 |
| UPSTREAMHUB_UPDATER_GITHUBREPO | `worryzyy/upstream-hub` | 升级检查的 GitHub 仓库。fork 自用时可改成自己的 `owner/repo`。 |
| UPSTREAMHUB_LOG_LEVEL | `info` | 日志等级。 |
| UPSTREAMHUB_LOG_FORMAT | `text` | 日志格式：`text` / `json`。 |
| **UPSTREAMHUB_HTTP_PORT** *(compose)* | `8080` | 宿主机映射端口（容器内仍是 8418）。 |
| **UPSTREAMHUB_IMAGE_TAG** *(compose)* | `edge` | compose 拉取的镜像 tag。 |

---

## 三、鉴权与公网暴露

- **纯内网 / 反代后**：`AUTH_ENABLED=false`（默认），所有 `/api/*` 免登录。前置 nginx/网关做 IP 白名单或 SSO。
- **公网暴露**：必须 `AUTH_ENABLED=true` + 强 `ADMIN_PASSWORD`。前端首次访问跳登录页，登录后下发 HMAC token（存 localStorage，默认 7 天有效，见 `auth.sessionTTLHours`）。

反向代理示例（nginx，把 8080 转到 443）：

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
- **SendMaxAttempts**：失败指数退避重试。

订阅规则可限制某通知渠道只接收指定上游或指定倍率分组的事件，详见 README。

---

## 七、数据持久化与备份

- PostgreSQL 数据在 `postgres-data` volume（compose）。备份：`docker exec upstreamhub-postgres pg_dump -U upstreamhub upstreamhub > backup.sql`。
- `APP_SECRET` 是数据加密根密钥——**备份密钥和数据库要一起保存**，缺任一都无法解密通知渠道的密钥字段。
- 历史数据清理由 `scheduler.retention` 控制，`rate_change_logs`（倍率变化记录）是业务核心数据，永久保留不清理。

---

## 八、健康检查与排障

- 健康检查：`GET /healthz`，返回 `{"status":"ok"}`。compose 用它做容器健康探针。
- 版本信息：`GET /api/version`。
- 日志：`docker compose logs -f app`。调高 `UPSTREAMHUB_LOG_LEVEL=debug` 看请求细节。
- 渠道同步失败：看 `monitor_logs` 表或面板「监控日志」，`error_message` 字段有具体原因（登录失败 / Turnstile / 上游超时等）。
- 升级失败：`docker compose logs app` 看下载/校验/替换日志。

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
A：这些只在启动时读取，改完 `docker compose restart app`。
