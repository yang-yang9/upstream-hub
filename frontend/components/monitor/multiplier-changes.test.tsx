/**
 * MultiplierChanges 渲染测试：加载/空态、变动条目（模型名、渠道名、类型角标、
 * 倍率箭头、涨跌幅、首见 "—" 与 "新增"、未关联渠道回退 #id）。
 *
 * queries.ts 的 cache 是模块级 Map 且 path 固定（/dashboard/summary），
 * 同文件多用例会串缓存：每个用例先 vi.resetModules() 再动态 import 拿全新模块。
 */
import { render, screen, waitFor } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  })
}

/** 按 URL 前缀分发桩数据。 */
function stubFetch(routes: Record<string, unknown>) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      for (const [prefix, body] of Object.entries(routes)) {
        if (url === prefix || url.startsWith(prefix)) return jsonResponse(body)
      }
      return jsonResponse({ data: null }, 404)
    }),
  )
}

async function renderMultiplier() {
  const { MultiplierChanges } = await import("./multiplier-changes")
  return render(<MultiplierChanges />)
}

describe("MultiplierChanges", () => {
  it("空数据渲染空态", async () => {
    vi.resetModules()
    stubFetch({
      "/api/dashboard/summary": { data: { recent_rate_changes: [] } },
      "/api/channels": { data: [] },
    })

    await renderMultiplier()
    await waitFor(() => expect(screen.getByText("暂无倍率变动记录")).toBeTruthy())
    expect(screen.getByText("最近倍率变动")).toBeTruthy()
  })

  it("渲染变动条目：渠道名/类型角标/倍率箭头/涨跌幅/首见回退", async () => {
    vi.resetModules()
    const now = new Date().toISOString()
    stubFetch({
      "/api/dashboard/summary": {
        data: {
          recent_rate_changes: [
            {
              id: 1,
              channel_id: 7,
              model_name: "gpt-4o",
              old_ratio: 1.2,
              new_ratio: 1.5,
              changed_at: now,
            },
            {
              id: 2,
              channel_id: 9,
              model_name: "claude",
              old_ratio: null,
              new_ratio: 2,
              changed_at: now,
            },
          ],
        },
      },
      "/api/channels": { data: [{ id: 7, name: "alpha", type: "newapi" }] },
    })

    await renderMultiplier()
    await waitFor(() => expect(screen.getByText("gpt-4o")).toBeTruthy())

    // 标题 + 条数。
    expect(screen.getByText("最近倍率变动")).toBeTruthy()
    expect(screen.getByText("2 条")).toBeTruthy()
    // 已关联渠道：名称 + NewAPI 角标。
    expect(screen.getByText("alpha")).toBeTruthy()
    expect(screen.getByText("NewAPI")).toBeTruthy()
    // 倍率箭头与涨跌幅（1.2 → 1.5 = +25.0%）。
    expect(screen.getByText("1.20")).toBeTruthy()
    expect(screen.getByText("1.50")).toBeTruthy()
    expect(screen.getByText("+25.0%")).toBeTruthy()
    // 未关联渠道回退 #channel_id；old_ratio 为 null：首见 "—" + "新增"。
    expect(screen.getByText("#9")).toBeTruthy()
    expect(screen.getByText("—")).toBeTruthy()
    expect(screen.getByText("新增")).toBeTruthy()
  })
})
