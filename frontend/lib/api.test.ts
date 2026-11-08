/**
 * apiFetch 特征测试：URL 前缀、鉴权头、{data} 解包、ApiError 形态、
 * X-Renewed-Token 滑动续期、401 登出流程。fetch 用 vi.stubGlobal 打桩。
 * 对应 spec：docs/specs/auth-session-token.md（滑动续期 / 401 行为的前端侧）。
 */
import { afterEach, describe, expect, it, vi } from "vitest"
import { apiFetch, getToken, setToken, setUnauthorizedHandler } from "./api"

function jsonResponse(body: unknown, status = 200, headers: Record<string, string> = {}) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json", ...headers },
  })
}

describe("apiFetch URL 前缀", () => {
  it("非 /api、非 http 开头的路径自动拼 /api 前缀", async () => {
    // mockImplementation 而非 mockResolvedValue：Response body 只能读一次，每次调用要新对象。
    const fetchMock = vi.fn().mockImplementation(async () => jsonResponse({ data: null }))
    vi.stubGlobal("fetch", fetchMock)

    await apiFetch("/channels")
    await apiFetch("channels") // 无前导斜杠也能补
    await apiFetch("/api/channels") // 已带前缀不重复拼
    await apiFetch("http://example.com/x") // 绝对地址不动

    expect(fetchMock.mock.calls.map((c) => c[0])).toEqual([
      "/api/channels",
      "/api/channels",
      "/api/channels",
      "http://example.com/x",
    ])
  })
})

describe("apiFetch 请求头", () => {
  afterEach(() => setUnauthorizedHandler(null))

  it("附带 Accept / Authorization / Content-Type（有 body 时）", async () => {
    setToken("tok-1")
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ data: null }))
    vi.stubGlobal("fetch", fetchMock)

    await apiFetch("/x", { method: "POST", body: JSON.stringify({ a: 1 }) })
    const headers = (fetchMock.mock.calls[0] as unknown[])[1] as RequestInit
    const h = headers.headers as Headers
    expect(h.get("Accept")).toBe("application/json")
    expect(h.get("Authorization")).toBe("Bearer tok-1")
    expect(h.get("Content-Type")).toBe("application/json")
  })

  it("无 body 不加 Content-Type；已带的 Authorization 不被覆盖", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ data: null }))
    vi.stubGlobal("fetch", fetchMock)

    await apiFetch("/x", { headers: { Authorization: "Basic abc" } })
    const h = (fetchMock.mock.calls[0] as unknown[])[1] as RequestInit
    const headers = h.headers as Headers
    expect(headers.get("Content-Type")).toBeNull()
    expect(headers.get("Authorization")).toBe("Basic abc")
  })
})

describe("apiFetch 响应解包", () => {
  it("{ data } 包裹的响应解出内层；扁平 JSON 原样返回", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValueOnce(jsonResponse({ data: { inner: 42 } }))
        .mockResolvedValueOnce(jsonResponse({ flat: true }))
        .mockResolvedValueOnce(jsonResponse(null))
        .mockResolvedValueOnce(new Response("just text", { status: 200 })),
    )

    expect(await apiFetch<{ inner: number }>("/a")).toEqual({ inner: 42 })
    expect(await apiFetch("/b")).toEqual({ flat: true })
    expect(await apiFetch("/c")).toBeNull() // body 为 null
    expect(await apiFetch("/d")).toBe("just text") // 纯文本回退
  })
})

describe("apiFetch 错误形态（ApiError）", () => {
  it("JSON error 字段 / 纯文本 / 空 body / 无 error 字段的 JSON", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValueOnce(jsonResponse({ error: "boom" }, 500))
        .mockResolvedValueOnce(new Response("plain failure", { status: 502 }))
        .mockResolvedValueOnce(new Response("", { status: 503 }))
        .mockResolvedValueOnce(jsonResponse({ oops: 1 }, 500)),
    )

    await expect(apiFetch("/e1")).rejects.toMatchObject({ status: 500, message: "boom" })
    await expect(apiFetch("/e2")).rejects.toMatchObject({ status: 502, message: "plain failure" })
    await expect(apiFetch("/e3")).rejects.toMatchObject({ status: 503, message: "HTTP 503" })
    await expect(apiFetch("/e4")).rejects.toMatchObject({ status: 500, message: "HTTP 500" })
  })

  it("抛出的是带 status / body 的 Error 实例", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ error: "bad" }, 422)))
    const err = await apiFetch("/x").catch((e: unknown) => e)
    expect(err).toBeInstanceOf(Error)
    expect((err as { status?: number }).status).toBe(422)
    expect((err as { body?: unknown }).body).toEqual({ error: "bad" })
  })
})

describe("滑动续期与 401 登出", () => {
  afterEach(() => setUnauthorizedHandler(null))

  it("响应带 X-Renewed-Token 时无感替换本地 token", async () => {
    setToken("old")
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(jsonResponse({ data: null }, 200, { "X-Renewed-Token": "renewed-1" })),
    )
    await apiFetch("/r")
    expect(getToken()).toBe("renewed-1")
  })

  it("401 清空 token 并触发全局 unauthorizedHandler", async () => {
    setToken("tok")
    const handler = vi.fn()
    setUnauthorizedHandler(handler)
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ error: "unauthorized" }, 401)))

    await expect(apiFetch("/x")).rejects.toMatchObject({ status: 401 })
    expect(getToken()).toBeNull()
    expect(handler).toHaveBeenCalledTimes(1)
  })

  it("skipAuthErrorHandler 的 401 不触发登出（登录页自己展示错误）", async () => {
    setToken("tok")
    const handler = vi.fn()
    setUnauthorizedHandler(handler)
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ error: "bad credentials" }, 401)))

    await expect(apiFetch("/login", { skipAuthErrorHandler: true })).rejects.toMatchObject({
      status: 401,
    })
    expect(handler).not.toHaveBeenCalled()
    expect(getToken()).toBe("tok")
  })
})
