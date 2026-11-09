/**
 * RuntimeSettingsCard 渲染 / 交互测试（P3-14，docs/specs/runtime-settings.md AC-6）。
 * TDD red：组件尚不存在（将在 components/settings/runtime-settings-card.tsx 实现）。
 * fetch 桩：GET /api/settings 预填 → PUT 保存全量 → 非法值不发请求 → DELETE 恢复默认。
 */
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { RuntimeSettingsCard } from "./runtime-settings-card"

const fullSettings = {
  balance_cron: "37 */15 * * * *",
  rate_cron: "13 */30 * * * *",
  retention_cron: "0 17 3 * * *",
  retention_monitor_logs_days: 30,
  retention_balance_snapshots_days: 90,
  retention_notification_logs_days: 90,
  batch_rate_changes: true,
  min_change_pct: 0.5,
  balance_low_cooldown_minutes: 60,
  failure_threshold: 3,
  failure_cooldown_minutes: 45,
  quiet_hours: "",
  send_max_attempts: 3,
  balance_recovery: false,
  forecast_window_days: 7,
  forecast_alert_days: 3,
  forecast_cooldown_minutes: 720,
  saved: false,
  updated_at: null as string | null,
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  })
}

function input(label: string): HTMLInputElement {
  return screen.getByLabelText(label) as HTMLInputElement
}

async function renderCard() {
  const fetchMock = vi.fn(async () => jsonResponse({ data: fullSettings }))
  vi.stubGlobal("fetch", fetchMock)
  render(<RuntimeSettingsCard />)
  await waitFor(() => expect(input("余额扫描 cron").value).toBe("37 */15 * * * *"))
  return fetchMock
}

describe("RuntimeSettingsCard", () => {
  it("按 调度 / 通知 / 预测 三组渲染字段，GET 当前生效值预填", async () => {
    await renderCard()

    expect(screen.getByText("调度")).toBeTruthy()
    expect(screen.getByText("通知")).toBeTruthy()
    expect(screen.getByText("预测")).toBeTruthy()
    // 预填：cron / 数值 / 文本各抽一个代表。
    expect(input("倍率扫描 cron").value).toBe("13 */30 * * * *")
    expect(input("失败阈值（次）").value).toBe("3")
    expect(input("静默时段").value).toBe("")
    expect(screen.getByText("未保存过")).toBeTruthy() // saved: false 状态标记
  })

  it("保存：PUT 提交全量 17 字段", async () => {
    const fetchMock = await renderCard()

    fireEvent.change(input("失败阈值（次）"), { target: { value: "5" } })
    fireEvent.click(screen.getByText("保存"))

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/settings", expect.objectContaining({ method: "PUT" })),
    )
    const calls = fetchMock.mock.calls as unknown as [string, RequestInit][]
    const putCall = calls.find((c) => c[1].method === "PUT")
    expect(putCall).toBeTruthy()
    const body = JSON.parse(putCall![1].body as string)
    expect(body.failure_threshold).toBe(5)
    expect(body.balance_cron).toBe("37 */15 * * * *")
    // 全量提交：17 个字段一个不少。
    expect(Object.keys(body).length).toBe(17)
  })

  it("非法 cron：行内提示且不发请求", async () => {
    const fetchMock = await renderCard()

    fireEvent.change(input("余额扫描 cron"), { target: { value: "*/15 * * * *" } }) // 5 段缺秒
    fireEvent.click(screen.getByText("保存"))

    await waitFor(() => expect(screen.getByText(/秒/)).toBeTruthy())
    const calls = fetchMock.mock.calls as unknown as [string, RequestInit][]
    const putCalls = calls.filter((c) => c[1]?.method === "PUT")
    expect(putCalls.length).toBe(0)
  })

  it("恢复默认：DELETE /api/settings", async () => {
    const fetchMock = await renderCard()

    fireEvent.click(screen.getByText("恢复默认"))

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/settings", expect.objectContaining({ method: "DELETE" })),
    )
  })
})
