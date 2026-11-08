# 开发手册（SDD + TDD 工作流）

本项目采用 **spec 驱动 + 测试驱动**的开发模式：先写规格、再写失败测试、再写实现。本文定义这个循环的具体做法和本地命令。

> 需求来源（缺失能力清单）见 [roadmap.md](roadmap.md)；部署运维见 [deployment.md](deployment.md)。

---

## 一、开发循环

每个需求（新功能 / 行为变更 / 修 bug）都走以下五步：

```
1. Spec    在 docs/specs/ 按模板写规格，验收标准（AC）必须是可测试的 Given-When-Then
2. Red     按 AC 写测试，先跑一遍确认失败（此时还没有实现，或行为尚未修正）
3. Green   写最小实现让测试通过
4. Refactor 在测试保护下重构；纯决策逻辑抽成无依赖的函数（便于单测）
5. Integrate 涉及持久化的行为补 env 门控的集成测试（见下文数据库一节）
```

规则：

- **Spec 是契约**。实现过程中发现规格写错，先改 spec（附变更记录）再改代码，不允许"代码悄悄偏离 spec"。
- **Red 必须真的红**。写完测试先跑，看到失败原因符合预期（编译错误 / 断言失败），而不是环境问题。
- **AC ↔ 测试一一对应**。spec 的每条 AC 和边界用例都能指到具体测试函数（spec 的"关联代码"章节回填）。
- **修 bug 也走循环**：先写一个复现 bug 的失败测试（这就是 AC），再修。

## 二、测试分层

| 层 | 目标 | 依赖 | 运行 |
|---|------|------|------|
| 纯函数单测 | 决策逻辑：变化检测、去抖阈值、订阅过滤、格式化 | 无 | `go test ./...` |
| 组件单测 | Dispatcher / Service 等组装逻辑 | 包内 fake（接口缝隙） | `go test ./...` |
| 集成测试 | storage 层 SQL 契约（UPSERT、聚合、冷却原子性） | 真 PostgreSQL | env 门控（下文） |
| 前端单测 | api 封装、格式化、hook、关键组件 | jsdom + fetch 桩 | `pnpm test` |

### 测试惯例（与存量代码保持一致）

- **不用 mock 库**（gomock/testify-mock 等）。外部 HTTP 依赖用 `httptest.NewServer` 伪造；包内依赖用接口缝隙 + 手写 fake。
- **白盒同包测试**测未导出逻辑（`package notify` 内直接测 `backoffDelay`）；跨包契约用外部测试包（`package storage_test`）。
- 纯逻辑优先用**表驱动**。
- 时间敏感行为不 `time.Sleep`：用可注入的时钟 / 预签 token / 显式 UTC 时间戳。
- 重试类测试把尝试次数压到 ≤2，真实退避曲线由纯函数测试覆盖，避免拖慢测试。

### 前端测试惯例

- **不引 msw / jest-dom / user-event**。fetch 用 `vi.stubGlobal("fetch", vi.fn())` 打桩，断言用 `getByText` / `waitFor` 原语。
- `Response` 的 body 只能读一次：桩里多次返回时用 `mockImplementation` 每次造新 `Response`，别复用同一个对象。
- `lib/queries.ts` 的 inflight / cache 是**模块级** Map（TTL 800ms）：同文件多用例会串缓存。固定 path 的组件测试用 `vi.resetModules()` + 动态 `import()` 拿全新模块；hook 测试用不同 path（如不同 `limit`）错开。
- `vitest.setup.ts` 统一做 afterEach 清理：RTL `cleanup()`、`vi.unstubAllGlobals()`、`localStorage.clear()`。
- 时间格式化断言传显式 `now` 参数；涉及本地时区的（`toLocaleDateString`）钉住具体期望值。

## 三、数据库集成测试

storage 层的 SQL（PostgreSQL 特有的 UPSERT、`date_trunc` 聚合）在 SQLite 上语义不同，因此集成测试**只跑真 PostgreSQL**，用环境变量门控：

```bash
# 门控开关（不设则所有集成测试 skip，go test ./... 保持绿）
export UPSTREAMHUB_TEST_DB=1

# DSN：优先 UPSTREAMHUB_TEST_DATABASE_DSN；未设时回落 config.Load("")
# （即 UPSTREAMHUB_DATABASE_* 系列变量继续可用）
export UPSTREAMHUB_TEST_DATABASE_DSN="host=localhost port=5432 user=uh password=uh dbname=uh_test sslmode=disable TimeZone=UTC"
```

本地起一次性测试库（需要 Docker）：

```bash
docker run -d --name uh-pg -p 5432:5432 \
  -e POSTGRES_USER=uh -e POSTGRES_PASSWORD=uh -e POSTGRES_DB=uh_test \
  postgres:16
```

跑全量（含集成）：

```bash
cd backend
UPSTREAMHUB_TEST_DB=1 UPSTREAMHUB_TEST_DATABASE_DSN="host=localhost port=5432 user=uh password=uh dbname=uh_test sslmode=disable TimeZone=UTC" \
  go test ./... -coverprofile=coverage.out
go tool cover -func=coverage.out | tail -1
```

隔离机制：`internal/storage/storagetest.Open(t)` 为每个测试打开连接池化的 `*gorm.DB`；harness 按调用方包自动创建独立数据库 `<dbname>_<包目录名>`（如 `uh_test_channel`），`go test ./...` 并行跑多个包的测试二进制时不会互相 TRUNCATE 踩踏，也保证 fallback DSN 指向的原始库不受影响。每个测试结束后 `TRUNCATE ... RESTART IDENTITY CASCADE` 清空全部表；包内测试串行执行。

## 四、本地命令速查

```bash
# 后端
cd backend
go vet ./... && go build ./...
go test ./...                    # 单测（集成 skip）
# 集成见上一节

# 前端
cd frontend
pnpm install
pnpm exec tsc --noEmit           # 类型检查
pnpm lint                        # eslint
pnpm build                       # 生产构建
pnpm test                        # vitest 单次
pnpm test:watch                  # vitest 监视
pnpm test:coverage               # 覆盖率（输出不设门槛）

# 冒烟
cd backend && go run ./cmd/server   # 需本地 PostgreSQL + 配置，见 deployment.md
```

## 五、提交与 CI

- CI（`.github/workflows/ci.yml`）在 dev push / PR 时跑：后端 vet/build/test（带 postgres service 跑集成）+ 前端 tsc/lint/build/test，并上传前后端覆盖率 artifact。
- 覆盖率**只输出不设门槛**；基线稳定后再考虑阈值。
- 提交信息格式沿用仓库现状（`feat:` / `fix:` / `docs:` / `perf:` 前缀）。

## 六、spec 目录索引

| Spec | 主题 |
|------|------|
| [channel-token-rotation.md](specs/channel-token-rotation.md) | Sub2API token 轮换持久化 |
| [auth-session-token.md](specs/auth-session-token.md) | 后台登录会话与续期 |
| [monitor-rate-scanning.md](specs/monitor-rate-scanning.md) | 余额 / 倍率扫描与变化检测 |
| [notification-dispatch.md](specs/notification-dispatch.md) | 通知分发、去抖与订阅过滤 |
| [scheduler-retention.md](specs/scheduler-retention.md) | 定时调度与历史数据清理 |
| [usage-forecast.md](specs/usage-forecast.md) | 余额消耗速率与到期预测 |
| [failure-recovery.md](specs/failure-recovery.md) | 失败风暴防刷屏与恢复通知 |
| [rate-compare.md](specs/rate-compare.md) | 跨渠道比价双视图 |
| [metrics-export.md](specs/metrics-export.md) | /metrics Prometheus 暴露 |
| [quiet-hours-cooldown.md](specs/quiet-hours-cooldown.md) | 静默时段与 per-channel 冷却覆盖 |
