import { afterEach, vi } from "vitest"
import { cleanup } from "@testing-library/react"

// 每个用例后统一清理：RTL 挂载的组件、vi.stubGlobal 打的桩、localStorage 里的 token。
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  window.localStorage.clear()
})
