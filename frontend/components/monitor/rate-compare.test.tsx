/**
 * RateCompare 渲染测试：加载/双空态、整体倍率排行（排序、最低倍率、起价分组、
 * 分组数）、同名分组比价（组头、价差、条目排序、最优标记）。
 *
 * queries.ts 的 cache 是模块级 Map，同文件多用例会串缓存：
 * 每个用例先 vi.resetModules() 再动态 import 拿全新模块。
 */
import { render, screen, waitFor } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  })
}

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

async function renderCompare() {
  const { RateCompare } = await import("./rate-compare")
  return render(<RateCompare />)
}

describe("RateCompare", () => {
  it("空数据渲染双空态", async () => {
    vi.resetModules()
    stubFetch({ "/api/rate-compare": { data: { channels: [], groups: [] } } })

    await renderCompare()
    await waitFor(() => expect(screen.getByText("跨渠道比价")).toBeTruthy())
    // 空态在 fetch resolve 后才渲染，waitFor 等待而非立取。
    await waitFor(() => expect(screen.getByText("暂无倍率数据")).toBeTruthy())
    expect(screen.getByText("暂无可比分组（同名分组需出现在 ≥2 个渠道）")).toBeTruthy()
  })

  it("渲染双视图：排行顺序 / 最低倍率 / 组头价差 / 最优标记", async () => {
    vi.resetModules()
    const now = new Date().toISOString()
    stubFetch({
      "/api/rate-compare": {
        data: {
          channels: [
            {
              channel_id: 1,
              channel_name: "alpha",
              channel_type: "newapi",
              min_ratio: 0.8,
              min_group: "vip",
              group_count: 2,
              last_seen_at: now,
            },
            {
              channel_id: 2,
              channel_name: "beta",
              channel_type: "sub2api",
              min_ratio: 1.2,
              min_group: "pro",
              group_count: 1,
              last_seen_at: now,
            },
          ],
          groups: [
            {
              model_name: "default",
              description: "基础分组",
              entries: [
                {
                  channel_id: 1,
                  channel_name: "alpha",
                  channel_type: "newapi",
                  ratio: 1.0,
                  last_seen_at: now,
                },
                {
                  channel_id: 2,
                  channel_name: "beta",
                  channel_type: "sub2api",
                  ratio: 1.2,
                  last_seen_at: now,
                },
              ],
              best_channel_id: 1,
              spread_pct: 20,
            },
          ],
        },
      },
    })

    await renderCompare()
    await waitFor(() => expect(screen.getByText("整体倍率排行")).toBeTruthy())

    // 视图 ①：标题 + 排行按最低倍率升序（alpha 先于 beta）+ 最低倍率与起价分组。
    expect(screen.getByText("同名分组比价")).toBeTruthy()
    const alphaIdx = document.body.textContent!.indexOf("alpha")
    const betaIdx = document.body.textContent!.indexOf("beta")
    expect(alphaIdx).toBeGreaterThanOrEqual(0)
    expect(alphaIdx).toBeLessThan(betaIdx)
    expect(screen.getByText("0.80")).toBeTruthy()
    expect(screen.getByText("vip")).toBeTruthy()
    expect(screen.getByText("2 个分组")).toBeTruthy()

    // 视图 ②：组名 + 描述 + 价差；条目倍率；最优标记恰好出现一次（alpha）。
    // "1.20" 在排行（beta min_ratio）与分组条目（beta ratio）各渲染一次。
    expect(screen.getByText("default")).toBeTruthy()
    expect(screen.getByText("基础分组")).toBeTruthy()
    expect(screen.getByText("20.0%")).toBeTruthy()
    expect(screen.getByText("1.00")).toBeTruthy()
    expect(screen.getAllByText("1.20")).toHaveLength(2)
    expect(screen.getAllByText("最优")).toHaveLength(1)
  })
})
