# Spec：跨渠道比价（零配置双视图）

> 选上游时真正的问题是"哪个站便宜、同一个分组在哪些站收多少倍"。倍率目前只在单渠道内自比（`rate_changed` 自己跟自己比）。本能力把 `rate_snapshots` 的现状数据横向聚合，交付**不需要任何配置**的两个视图：① 整体倍率排行（全渠道可比，无对齐问题）② 同名分组比价（归一化字面匹配）。

## 背景

现状（勘察确认）：

- `rate_snapshots` 表 upsert per (channel_id, model_name)，保存每个渠道当前的 `ratio` / `completion_ratio` / `description` / `last_seen_at`——数据已齐备，只差横向聚合。
- **`completion_ratio` 当前恒为 0**：两个 connector 的 `GetRates` 都只填 `Ratio`（NewAPI `/api/user/self/groups` 只有 desc + ratio；Sub2API `groups/available` 同样只有倍率）。字段与表结构保留，但本期排序与展示不使用它。
- **分组跨站没有可靠自动对齐**：分组名是各站点自定义的计价单位；用户侧接口拿不到"分组→模型"映射（NewAPI `/api/user/models` 只返回用户自己所属组的模型，管理侧数据不可达）。因此除字面同名外不存在可靠的零成本等价判定，模糊猜名（"基础组"≈"default"）猜错会直接误导比价，不做。
- 两条不需要名字对齐的可比路径：
  - **整体倍率**：每渠道取其最低倍率。单分组渠道（生态里很常见，很多站只有 default 一个组）的最低倍率就是该站整体定价；多分组渠道的最低倍率是该站"起价"。全渠道直接可比。
  - **同名分组**：NewAPI / Sub2API 生态大量站点沿用 default / vip / svip 这套惯例命名，归一化后字面匹配可覆盖。自造命名的分组不硬凑，不出现在该视图。

## 行为规格

### AC-1: 整体倍率排行（视图 ①，纯函数）

- **Given** 全渠道 `rate_snapshots` + 渠道元信息（id → name / type）
- **When** 聚合为渠道级排行
- **Then** 每个有 ≥1 条快照的渠道输出一条：`channel_id` / `channel_name` / `channel_type` / `min_ratio`（该渠道最低 ratio）/ `min_group`（取到最低值的分组名）/ `group_count` / `last_seen_at`（该渠道最新快照时间）；按 `min_ratio` 升序（并列按渠道名稳定序）；无快照的渠道不出现在排行中

### AC-2: 同名分组比价（视图 ②，纯函数）

- **Given** 全渠道 `rate_snapshots` + 渠道元信息 + `min_channels`（默认 2）
- **When** 按 `normalizeGroupName(model_name)`（TrimSpace + ToLower）聚合
- **Then** 输出分组列表：每项含 `model_name`（展示首个非归一化原始名）、`description`（条目里第一个非空，按条目排序确定）、`entries`（各渠道一条：channel_id / channel_name / channel_type / ratio / last_seen_at）；条目按 ratio 升序（并列渠道名稳定序）；每项带 `best_channel_id`（ratio 最低者）与 `spread_pct`（(最高−最低)/最低 × 100，四舍五入 1 位小数，单条目为 0）；归一化后出现在 < min_channels 个渠道的分组被过滤；分组列表按条目数降序、归一化名升序

### AC-3: API 端点

- **Given** 已有渠道与快照数据
- **When** `GET /api/rate-compare?min_channels=N`
- **Then** 200 + `{"data": {"channels": [排行], "groups": [分组比价]}}`；`min_channels` 缺省 2，非法值（<1、非数字）回退 2；仅影响 groups 视图；无数据时两数组为空

### AC-4: 前端比价面板

- **Given** Dashboard
- **When** 渲染比价组件
- **Then** 新面板"跨渠道比价"含两段：**整体倍率排行**（渠道行：渠道名、类型标签、最低倍率高亮、起价分组名、分组数、相对时间，最低者排最前）与**同名分组比价**（每组头显示分组名 + 描述 + 价差，组内按倍率升序列渠道行，最优渠道行高亮 success 色）；任一视图空态提示（排行空："暂无倍率数据"；分组空："暂无可比分组（同名分组需出现在 ≥2 个渠道）"）

## 边界用例

| 边界 | 期望 | 测试 |
|------|------|------|
| 快照为空 | 两视图均空数组，不报错 | 纯函数表驱动 |
| 渠道只有一个分组 | 视图 ① 正常参与（min_ratio 即整体倍率） | 纯函数表驱动 |
| 归一化后同名但原始大小写不同（"Default" vs "default"） | 归入同一分组，展示名取第一个非归一化原始名 | 纯函数表驱动 |
| 同分组多渠道 ratio 并列 | 渠道名稳定序，best_channel_id 取排序第一条 | 纯函数表驱动 |
| 单渠道分组（min_channels=2 时） | groups 视图过滤；`min_channels=1` 时保留 | 纯函数表驱动 |
| min_channels 非法（0 / -1 / "abc"） | API 层回退 2 | API 测试 |
| 快照的 channel_id 对不上 channels 列表（渠道已删的残留） | 该快照丢弃，不崩溃 | 纯函数表驱动 |
| monitor_enabled=false 的渠道 | 仍参与（快照是事实，last_seen_at 变旧自然可见） | 行为说明，不单测 |
| 分组最低 ratio = 0（免费分组） | 价差在零基准上无定义，`spread_pct` 取 0 | 纯函数表驱动 |
| 同分组同渠道脏数据多条（unique index 下不该出现） | 不做去重防御，条目按输入出现 | 行为说明，不单测 |

## 非目标

- 不做别名 / 相似名建议合并机制（自造分组名的跨站归组唯一诚实做法是"自动建议 + 用户一次确认、规则入库"，属后续独立 spec）。
- 不做 completion_ratio 参与排序或展示（当前恒 0；将来 connector 开始上报时另开修订）。
- 不做 per-model 比价（需要采集分组→模型映射，用户侧接口不可达，属 P2 数据面扩展）。
- 不做比价通知 / 告警（比价是查询视图；"A 比 B 便宜了 X%"告警属 P1，独立 spec）。
- 不做 URL 参数筛选 / 分组深链接。
- 不修改 `ChannelRatesPanel` 既有视图（纵向看单渠道全貌、横向比价并存）。

## 关联代码

- 纯逻辑：`backend/internal/monitor/compare.go` — `buildRateCompare`（AC-1/AC-2 聚合）/ `normalizeGroupName`（归一化）/ `spreadPct`（价差）；类型 `ChannelRateRank` / `RateCompareEntry` / `RateCompareGroup` / `RateCompareResult`
- 服务接线：`backend/internal/monitor/service.go` — `Service.RateCompare(minChannels)`（现算，不落库）
- 存储：`backend/internal/storage/rates.go` — `Rates.ListAll()`（全量快照读取）
- API：`backend/internal/api/rates.go` — `GET /rate-compare`（min_channels 缺省 2，非法回退 2）
- 前端：`frontend/components/monitor/rate-compare.tsx`（面板）；`frontend/app/page.tsx`（ChannelCards 与 BottomPanels 之间挂载）；`frontend/lib/queries.ts` — `useRateCompare`；`frontend/lib/api-types.ts` — 四个比价类型
- 单元测试：`backend/internal/monitor/compare_test.go`（表驱动，AC-1/AC-2 + 边界）
- API 集成测试：`backend/internal/api/compare_test.go`（`UPSTREAMHUB_TEST_DB=1` 门控，AC-3 + min_channels 边界）
- 前端测试：`frontend/components/monitor/rate-compare.test.tsx`（双空态 + 双视图渲染，AC-4）

## 变更记录

- 2026-11-08 — 初稿（P0-2，待评审）：原设计为"同名分组比价"单视图 + 手配 yaml 别名映射
- 2026-11-08 — 重构（P0-2，待评审）：勘察确认 completion_ratio 恒零、分组→模型映射用户侧不可达后，改为零配置双视图（整体倍率排行 + 同名分组比价），别名机制移入非目标
- 2026-11-08 — 实现完成（P0-2）：三面红→绿（monitor 纯函数 / API 端点 / 前端面板），关联代码回填如上
