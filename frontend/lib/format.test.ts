/**
 * format 纯函数表驱动特征测试：相对时间、金额、倍率格式化、涨跌幅、类型标签。
 * 对应 spec：docs/specs/monitor-rate-scanning.md（倍率变动的展示语义）。
 */
import { describe, expect, it } from "vitest"
import {
  channelTypeLabel,
  formatRatio,
  money,
  ratioArrow,
  ratioDelta,
  relativeTime,
  shortTime,
} from "./format"

const NOW = new Date("2026-01-15T12:00:00Z")

describe("relativeTime", () => {
  const cases: Array<{ name: string; iso: string | null | undefined; want: string }> = [
    { name: "null", iso: null, want: "—" },
    { name: "undefined", iso: undefined, want: "—" },
    { name: "非法字符串", iso: "not-a-date", want: "—" },
    { name: "2 秒前", iso: new Date(NOW.getTime() - 2_000).toISOString(), want: "刚刚" },
    { name: "4 秒前（<5s 阈值）", iso: new Date(NOW.getTime() - 4_000).toISOString(), want: "刚刚" },
    { name: "未来时间（钳到 0）", iso: new Date(NOW.getTime() + 10_000).toISOString(), want: "刚刚" },
    { name: "30 秒前", iso: new Date(NOW.getTime() - 30_000).toISOString(), want: "30 秒前" },
    { name: "5 分钟前", iso: new Date(NOW.getTime() - 5 * 60_000).toISOString(), want: "5 分钟前" },
    { name: "59 分钟前", iso: new Date(NOW.getTime() - 59 * 60_000).toISOString(), want: "59 分钟前" },
    { name: "3 小时前", iso: new Date(NOW.getTime() - 3 * 3_600_000).toISOString(), want: "3 小时前" },
    { name: "23 小时前", iso: new Date(NOW.getTime() - 23 * 3_600_000).toISOString(), want: "23 小时前" },
    { name: "3 天前", iso: new Date(NOW.getTime() - 3 * 86_400_000).toISOString(), want: "3 天前" },
    { name: "29 天前", iso: new Date(NOW.getTime() - 29 * 86_400_000).toISOString(), want: "29 天前" },
    { name: "40 天前（回落到日期）", iso: "2025-12-06T12:00:00Z", want: "2025/12/6" },
  ]
  for (const tc of cases) {
    it(tc.name, () => {
      expect(relativeTime(tc.iso, NOW)).toBe(tc.want)
    })
  }
})

describe("money", () => {
  const cases: Array<{ name: string; value: number | null | undefined; want: string; precise?: boolean }> = [
    { name: "null", value: null, want: "—" },
    { name: "undefined", value: undefined, want: "—" },
    { name: "NaN", value: Number.NaN, want: "—" },
    { name: "Infinity", value: Number.POSITIVE_INFINITY, want: "—" },
    { name: "零", value: 0, want: "$0.00" },
    { name: "默认 2 位小数", value: 1234.5, want: "$1,234.50" },
    { name: "precise 4 位小数", value: 1234.5, want: "$1,234.5000", precise: true },
    { name: "precise 千分位", value: 1234.5678, want: "$1,234.5678", precise: true },
  ]
  for (const tc of cases) {
    it(tc.name, () => {
      expect(money(tc.value, { precise: tc.precise })).toBe(tc.want)
    })
  }
})

describe("formatRatio", () => {
  const cases: Array<{ name: string; value: number; want: string }> = [
    { name: "整数补 2 位", value: 2, want: "2.00" },
    { name: "零", value: 0, want: "0.00" },
    { name: "1 位小数补齐", value: 1.2, want: "1.20" },
    { name: "2 位小数保持", value: 1.5, want: "1.50" },
    { name: "4 位小数保持", value: 1.2345, want: "1.2345" },
    { name: "第 5 位四舍五入", value: 1.23456, want: "1.2346" },
  ]
  for (const tc of cases) {
    it(tc.name, () => {
      expect(formatRatio(tc.value)).toBe(tc.want)
    })
  }
})

describe("ratioArrow", () => {
  it("首见（from 为 null）渲染 — → 新值", () => {
    expect(ratioArrow(null, 1.5)).toBe("— → 1.50")
  })
  it("常规渲染 旧 → 新", () => {
    expect(ratioArrow(1.2, 1.5)).toBe("1.20 → 1.50")
  })
})

describe("ratioDelta", () => {
  const cases: Array<{ name: string; from: number | null; to: number; direction: string; pct: string }> = [
    { name: "null 视为首见", from: null, to: 3, direction: "up", pct: "新增" },
    { name: "0 视为首见", from: 0, to: 3, direction: "up", pct: "新增" },
    { name: "上涨", from: 2, to: 2.5, direction: "up", pct: "+25.0%" },
    { name: "下跌", from: 2, to: 1, direction: "down", pct: "-50.0%" },
    { name: "持平按上涨渲染", from: 2, to: 2, direction: "up", pct: "+0.0%" },
    { name: "负基数按绝对值算", from: -2, to: 2, direction: "up", pct: "+200.0%" },
  ]
  for (const tc of cases) {
    it(tc.name, () => {
      expect(ratioDelta(tc.from, tc.to)).toEqual({ direction: tc.direction, pct: tc.pct })
    })
  }
})

describe("channelTypeLabel", () => {
  it("newapi/sub2api 有显示名，其它原样透传", () => {
    expect(channelTypeLabel("newapi")).toBe("NewAPI")
    expect(channelTypeLabel("sub2api")).toBe("Sub2API")
    expect(channelTypeLabel("other")).toBe("other")
  })
})

describe("shortTime", () => {
  it("空 / 非法输入返回占位符", () => {
    expect(shortTime(null)).toBe("—")
    expect(shortTime("bad")).toBe("—")
  })
})
