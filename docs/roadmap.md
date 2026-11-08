# 能力现状与缺失清单（Roadmap）

本文梳理 upstream-hub 当前已具备的监控能力，并给出尚缺失的能力清单，作为后续迭代的规划基线。

> 部署、环境变量、排障见 [deployment.md](deployment.md)，本文不重复。

---

## 一、现状：已具备的能力

### 1.1 渠道接入层（`backend/internal/connector`）

- 插件式 Connector 注册机制（`connector.For(type)`），已有 NewAPI、Sub2API 两种实现
- 统一接口：Turnstile sitekey 探测 / Login / CheckAuth / GetBalance / GetRates
- 两种凭据模式：`password`（完整登录流程）、`token`（直接存 cookie / access_token，跳过登录）
- Sub2API refresh_token 自动轮换（JWT 过期检测后换新 token 对）
- 每渠道独立出站代理 + transport 缓存
- Turnstile 打码接入 4 家平台：CapSolver / 2Captcha / AntiCaptcha / YesCaptcha，支持自定义 endpoint

### 1.2 监控采集层（`backend/internal/monitor` + `backend/internal/scheduler`）

- 余额扫描：cron 可配，写 `balance_snapshots` 快照 + 渠道表聚合字段
- 余额消耗预测：对窗口内快照做线性回归得日均消耗，预测耗尽时点并提前告警（`balance_forecast_low`）
- 倍率扫描：`rate_snapshots` upsert 当前态，差异写 `rate_change_logs`（ratio + completion_ratio 双维度比对）
- 每渠道可独立开关监控、设余额阈值；单渠道失败不影响其他
- 每次采集写 `monitor_logs`（成功 / 失败、耗时、错误信息）
- 历史数据保留策略：monitor_logs / balance_snapshots / notification_logs 定期清理
- 手动触发刷新 + SSE 实时进度推送

### 1.3 通知层（`backend/internal/notify`）

- 7 种通知渠道：Telegram / Webhook / Email(SMTP) / 企业微信 / 钉钉 / 飞书 / Bark
- 8 类事件：`balance_low` / `balance_forecast_low` / `rate_changed` / `login_failed` / `captcha_failed` / `monitor_failed` / `login_recovered` / `monitor_recovered`
- 通知渠道配置 AES-GCM 加密落库
- 去抖：balance_low / balance_forecast_low 双事件独立冷却（PostgreSQL 持久化、跨重启）、同批倍率变化合并推送、MinChangePct 涨跌幅过滤（低于阈值仍写库、不推送）
- 失败风暴防护：连续 N 次失败才告警（按 job 维度计数，`failure_streaks` 表持久化）+ 失败事件冷却 + 成功后"已恢复"通知
- 按通知渠道的订阅过滤（关心哪些上游 / 分组）、指数退避重试、发送日志、测试发送

### 1.4 展示与运维

- Dashboard 聚合视图：总余额 / 最低余额 / active-failed 渠道数 / 最近倍率变化 / 最近通知
- 余额趋势按小时 / 天聚合；渠道详情含倍率列表、余额历史、最近错误；渠道卡片含余额消耗预测
- 面板内自升级：检查 GitHub release → 下载 → checksum 校验 → 换二进制 → 重启（支持 GITHUB_PROXY）
- 单用户后台登录：账号密码 + HMAC token + 滑动续期 + 绝对上限
- `/healthz` 健康检查（含 DB ping）；Docker Compose / 单二进制部署

---

## 二、缺失能力清单

按优先级分四档：**P0 高价值且数据已具备**、**P1 告警闭环**、**P2 监控维度扩展**、**P3 调度 / 自身可观测 / 安全加固**。

### P0 · 把已有数据用起来

| # | 能力 | 说明 |
|---|------|------|
| 1 | ~~余额消耗速率 & 到期预测~~（已完成，spec 见 [specs/usage-forecast.md](specs/usage-forecast.md)） | `balance_snapshots` 时间序列已在库中但未利用。做线性回归计算日均消耗，预测"N 天后耗尽"并提前告警。比静态阈值实用：阈值是猜的，消耗速度是测的 |
| 2 | ~~跨渠道比价~~（已完成，spec 见 [specs/rate-compare.md](specs/rate-compare.md)） | 倍率变化只在单渠道内自比。交付零配置双视图：整体倍率排行（每渠道最低倍率，全渠道可比）+ 同名分组比价（归一化字面匹配，最优渠道高亮与价差）。分组名跨站无可靠自动对齐，不硬凑 |
| 3 | 告警恢复通知 | 只有触发、没有恢复。余额回升、错误清除后不发"已恢复"，用户无法区分"还在异常"和"已经好了" |

### P1 · 告警体系闭环

| # | 能力 | 说明 |
|---|------|------|
| 4 | ~~失败风暴防刷屏~~（已完成，spec 见 [specs/failure-recovery.md](specs/failure-recovery.md)） | 失败事件冷却 + "连续 N 次失败才告警"（按 job 维度计数）+ 恢复通知（`login_recovered` / `monitor_recovered`） |
| 5 | 通知渠道自身健康 | 发送失败只写 `notification_logs`。Telegram token 失效这类问题需要兜底机制（如备用通知渠道）被发现 |
| 6 | 静默时段 / 每事件独立冷却 | 无 quiet hours；`BalanceLowCooldown` 全局一份，缺 per-event / per-channel 粒度 |
| 7 | 日报 / 周报 digest | 余额、倍率变动、失败汇总的定时摘要。通知框架已就绪，只差一个 cron 任务 |

### P2 · 监控维度扩展

| # | 能力 | 说明 |
|---|------|------|
| 8 | ~~API key 直连上游~~（不需要，暂不规划） | 只支持"面板站账号"模式。OpenRouter / DeepSeek / 官方 API 这类用 sk-xxx 查 quota 的直连上游没有 Connector。这类形态不需要打码 / 登录，接入成本反而低 |
| 9 | 可用性拨测 | 无独立的存活 / 延迟探测。站点登录页 5xx、API 延迟恶化这类"站还在但变慢 / 变坏"的信号采集不到（`monitor_logs.DurationMS` 有数据但未展示、未告警） |
| 10 | 模型可用性监控 | 上游悄悄下架 / 降智模型（返回 4xx）监控不到 |
| 11 | 更多面板站类型 | one-api / done-hub 等兼容面板站；以及 hCaptcha / reCAPTCHA 打码（当前只支持 Turnstile） |

### P3 · 调度、可观测与安全加固

| # | 能力 | 说明 |
|---|------|------|
| 12 | 并发扫描 | `ScanAllBalances` / `ScanAllRates` 是顺序 for 循环，渠道一多单轮时间线性膨胀（5 分钟硬超时可能被打满）。`SchedulerConfig.Concurrency` 字段存在但未使用 |
| 13 | 失败退避 | 登录失败后每轮照常重试，打码平台费用被持续消耗。需要失败渠道的指数退避（如连续失败后按 2×、4×、8× 轮次拉长间隔） |
| 14 | 配置热更新 | cron、通知 Policy、保留策略全在 env / config，改调度参数必须重启。可改为运行时 UI 可改并落库 |
| 15 | `/metrics` 暴露 | 无法接入现成的 Prometheus / Grafana / 告警体系。对已有可观测基建的用户是接入断点 |
| 16 | 数据导出 | 倍率变化、余额历史不能导 CSV / JSON；渠道配置不能备份导出（换机迁移只能拷数据库） |
| 17 | 出站 SSRF 校验 | updater 下载有 `validateDownloadURL`，notify 侧 Webhook 任意 URL 缺内网地址拦截，应对称补齐 |
| 18 | 密钥轮换与登录加固 | APP_SECRET（加密密钥）无轮换路径；登录无失败锁定 / 2FA；无只读分享（只读 token / 公开只读面板），给同事看余额只能给管理员账号 |

---

## 三、建议实施顺序

```
P0-1 消耗预测（✅ 已完成） → P1-4 失败冷却/恢复（✅ 已完成） → P0-2 跨渠道比价（✅ 已完成）
    → P3-15 /metrics → 其余按需
```

理由：P0 三项纯逻辑补齐、不新增数据依赖，性价比最高；P1-4 防刷屏是告警可靠性的前提；`/metrics` 打通已有可观测体系。
