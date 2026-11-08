import { describe, expect, it } from "vitest"

import {
  buildNotifyCooldowns,
  parseNotifyCooldowns,
  type CooldownFormValues,
} from "@/lib/notify-cooldowns"

// 对应 spec：docs/specs/quiet-hours-cooldown.md AC-6。
// 表单侧的覆盖 JSON 编解码：回填解析（坏数据 fail-open）、提交序列化（只带非空项）。

describe("parseNotifyCooldowns", () => {
  it("空值返回空对象", () => {
    expect(parseNotifyCooldowns(undefined)).toEqual({})
    expect(parseNotifyCooldowns(null)).toEqual({})
    expect(parseNotifyCooldowns("")).toEqual({})
    expect(parseNotifyCooldowns("{}")).toEqual({})
  })

  it("解析合法 JSON", () => {
    expect(parseNotifyCooldowns('{"balance_low":10,"login_failed":5}')).toEqual({
      balance_low: 10,
      login_failed: 5,
    })
  })

  it("坏 JSON fail-open 返回空对象（回填为空）", () => {
    expect(parseNotifyCooldowns("not json")).toEqual({})
  })
})

describe("buildNotifyCooldowns", () => {
  const empty: CooldownFormValues = {
    balance_low: "",
    balance_forecast_low: "",
    login_failed: "",
    monitor_failed: "",
  }

  it("全空返回空串（不覆盖，跟随全局）", () => {
    expect(buildNotifyCooldowns(empty)).toBe("")
  })

  it("只序列化非空项为数字", () => {
    const v = { ...empty, balance_low: "10", monitor_failed: "5" }
    expect(JSON.parse(buildNotifyCooldowns(v))).toEqual({
      balance_low: 10,
      monitor_failed: 5,
    })
  })

  it("0 是合法值（显式关闭该事件冷却）", () => {
    const v = { ...empty, login_failed: "0" }
    expect(JSON.parse(buildNotifyCooldowns(v))).toEqual({ login_failed: 0 })
  })

  it("非数字 / 负数输入跳过该键", () => {
    const v = { ...empty, balance_low: "abc", login_failed: "-5", monitor_failed: "3" }
    expect(JSON.parse(buildNotifyCooldowns(v))).toEqual({ monitor_failed: 3 })
  })

  it("空格输入视为空", () => {
    const v = { ...empty, balance_low: "  " }
    expect(buildNotifyCooldowns(v)).toBe("")
  })
})
