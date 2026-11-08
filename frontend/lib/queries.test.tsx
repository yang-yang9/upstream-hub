/**
 * useApi 数据获取 hook 测试：loading→data 流转、800ms TTL 缓存去重、
 * refetch 后台拉新、错误分支。
 *
 * 注意：queries.ts 的 inflight / cache 是模块级 Map，跨用例会串味——
 * 每个用例用不同的 path（不同 limit）规避污染。
 * 对应 spec：docs/specs/monitor-rate-scanning.md（前端拉取契约）。
 */
import { renderHook, waitFor, act } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { useRateChanges } from "@/lib/queries"

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  })
}

describe("useApi", () => {
  it("初次加载：loading → data", async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(jsonResponse({ data: [{ id: 1 }] }))
    vi.stubGlobal("fetch", fetchMock)

    const { result } = renderHook(() => useRateChanges(101))
    expect(result.current.loading).toBe(true)
    expect(result.current.data).toBeNull()

    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.data).toEqual([{ id: 1 }])
    expect(result.current.error).toBeNull()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect((fetchMock.mock.calls[0] as unknown[])[0]).toBe("/api/rate-changes?limit=101")
  })

  it("800ms TTL 内重挂载命中缓存，不重复 fetch", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ data: [] }))
    vi.stubGlobal("fetch", fetchMock)

    const first = renderHook(() => useRateChanges(102))
    await waitFor(() => expect(first.result.current.data).toEqual([]))
    first.unmount()

    const second = renderHook(() => useRateChanges(102))
    await waitFor(() => expect(second.result.current.data).toEqual([]))

    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it("refetch 触发新请求并更新数据（同 path 新 key）", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ data: [{ id: 1 }] }))
      .mockResolvedValueOnce(jsonResponse({ data: [{ id: 1 }, { id: 2 }] }))
    vi.stubGlobal("fetch", fetchMock)

    const { result } = renderHook(() => useRateChanges(103))
    await waitFor(() => expect(result.current.data).toEqual([{ id: 1 }]))

    act(() => result.current.refetch())
    await waitFor(() => expect(result.current.data).toEqual([{ id: 1 }, { id: 2 }]))
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it("请求失败进入 error 分支", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ error: "boom" }), { status: 500 }))
    vi.stubGlobal("fetch", fetchMock)

    const { result } = renderHook(() => useRateChanges(104))
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.data).toBeNull()
    expect(result.current.error).toBe("boom")
  })
})
