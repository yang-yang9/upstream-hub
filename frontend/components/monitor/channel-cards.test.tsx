/**
 * ForecastRow 渲染测试：消耗预测行（日均消耗 + 耗尽文案）、非消耗渠道不渲染、
 * 请求失败静默。对应 spec：docs/specs/usage-forecast.md AC-7。
 *
 * queries.ts 的 cache 是模块级 Map，同文件多用例会串缓存：
 * 每个用例先 vi.resetModules() 再动态 import 拿全新模块（路径固定 /api/channels/1/forecast）。
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

const FORECAST_PATH = "/api/channels/1/forecast"

async function renderForecastRow() {
  const { ForecastRow } = await import("./channel-cards")
  return render(<ForecastRow channelID={1} />)
}

describe("ForecastRow", () => {
  it("消耗中的渠道显示日均消耗与耗尽时间", async () => {
    vi.resetModules()
    stubFetch({
      [FORECAST_PATH]: {
        data: {
          channel_id: 1,
          daily_burn: 20,
          days_left: 3.5,
          depleted_at: "2026-01-19T00:00:00Z",
          samples: 672,
          window_days: 7,
          depleting: true,
        },
      },
    })

    await renderForecastRow()
    // Row 的值是组合文本 "$20.00/天 · 预计 3.5 天后耗尽"，用正则做子串匹配。
    await waitFor(() => expect(screen.getByText(/\$20\.00\/天/)).toBeTruthy())
    expect(screen.getByText(/3\.5 天后耗尽/)).toBeTruthy()
  })

  it("已耗尽显示对应文案", async () => {
    vi.resetModules()
    stubFetch({
      [FORECAST_PATH]: {
        data: {
          channel_id: 1,
          daily_burn: 20,
          days_left: 0,
          depleted_at: "2026-01-01T00:00:00Z",
          samples: 100,
          window_days: 7,
          depleting: true,
        },
      },
    })

    await renderForecastRow()
    await waitFor(() => expect(screen.getByText(/已耗尽/)).toBeTruthy())
  })

  it("非消耗渠道不渲染预测行", async () => {
    vi.resetModules()
    stubFetch({
      [FORECAST_PATH]: {
        data: {
          channel_id: 1,
          daily_burn: 0,
          days_left: 0,
          depleted_at: null,
          samples: 500,
          window_days: 7,
          depleting: false,
        },
      },
    })

    const { container } = await renderForecastRow()
    // 等加载结束（fetch 已 resolve），组件仍不应渲染任何内容。
    await waitFor(() =>
      expect(vi.mocked(fetch).mock.calls.some(([u]) => u === FORECAST_PATH)).toBeTruthy(),
    )
    await new Promise((r) => setTimeout(r, 10))
    expect(container.textContent).toBe("")
  })

  it("请求失败静默：不渲染、不抛错", async () => {
    vi.resetModules()
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        throw new Error("boom")
      }),
    )

    const { container } = await renderForecastRow()
    await new Promise((r) => setTimeout(r, 10))
    expect(container.textContent).toBe("")
  })
})
