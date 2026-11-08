/**
 * KpiRow 渲染测试：KPI 标签、金额格式化、渠道健康/失败计数、今日倍率变动计数。
 * fetch 按路由分发桩数据；今日计数按本地 0 点切分（跨天种子用 -26h 保证落在昨天）。
 */
import { render, screen, waitFor } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { KpiRow } from "./kpi-row"

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  })
}

describe("KpiRow", () => {
  it("渲染三张 KPI 卡：总余额 / 渠道状态 / 今日倍率变动", async () => {
    const now = new Date()
    const changes = [
      { id: 1, model_name: "a", old_ratio: null, new_ratio: 1, changed_at: now.toISOString() },
      { id: 2, model_name: "b", old_ratio: 1, new_ratio: 2, changed_at: now.toISOString() },
      // 昨天 26 小时前：不计入今日。
      {
        id: 3,
        model_name: "c",
        old_ratio: 1,
        new_ratio: 3,
        changed_at: new Date(now.getTime() - 26 * 3_600_000).toISOString(),
      },
    ]
    const summary = {
      total_channels: 3,
      active_channels: 2,
      failed_channels: 1,
      total_balance: 1234.5,
      lowest_balance: { name: "low", balance: 1.23 },
      recent_rate_changes: [],
      recent_notification_logs: [],
    }

    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        if (url === "/api/dashboard/summary") return jsonResponse({ data: summary })
        if (url.startsWith("/api/rate-changes")) return jsonResponse({ data: changes })
        return jsonResponse({ data: null }, 404)
      }),
    )

    render(<KpiRow />)

    await waitFor(() => expect(screen.getByText("$1,234.50")).toBeTruthy())
    expect(screen.getByText("总余额")).toBeTruthy()
    expect(screen.getByText("渠道状态")).toBeTruthy()
    expect(screen.getByText("今日倍率变动")).toBeTruthy()
    // 渠道状态 footer：2/3 健康 + 1 失败。
    expect(screen.getByText("2 健康")).toBeTruthy()
    expect(screen.getByText("1 失败")).toBeTruthy()
    // 最低余额渠道。
    expect(screen.getByText("low")).toBeTruthy()
    expect(screen.getByText("$1.23")).toBeTruthy()
    // 今日变动计数 = 2（昨天那条不计入）。
    expect(screen.getByText("检测到 2 次变动")).toBeTruthy()
  })
})
