/**
 * per-channel 通知冷却覆盖（P1-6）的表单编解码。
 *
 * 后端契约见 backend/internal/notify/cooldown.go：渠道 channels.notify_cooldowns
 * 存 JSON 对象，键限 4 类参与冷却的事件，值 = 分钟（int >= 0，0 = 显式关闭
 * 该事件的冷却）。空串 / "{}" = 不覆盖，跟随全局 Policy。
 * 对应 spec：docs/specs/quiet-hours-cooldown.md AC-6。
 */

/** 4 类冷却事件在表单里的字段（值为字符串形式的分钟数，"" = 留空跟随全局）。 */
export interface CooldownFormValues {
  balance_low: string
  balance_forecast_low: string
  login_failed: string
  monitor_failed: string
}

/** 表单输入框的展示标签（与监控面板事件文案一致）。 */
export const COOLDOWN_EVENT_LABELS: Record<keyof CooldownFormValues, string> = {
  balance_low: "余额低于阈值",
  balance_forecast_low: "预计耗尽",
  login_failed: "登录失败",
  monitor_failed: "采集失败",
}

/**
 * 解析渠道的 notify_cooldowns JSON 用于回填。坏数据 fail-open 返回空对象
 * （表单显示为空，保存时会被清掉 / 重新覆盖），与后端 monitor 侧语义一致。
 */
export function parseNotifyCooldowns(raw: string | null | undefined): Record<string, number> {
  if (!raw) return {}
  try {
    const obj: unknown = JSON.parse(raw)
    if (obj === null || typeof obj !== "object" || Array.isArray(obj)) {
      return {}
    }
    return obj as Record<string, number>
  } catch {
    return {}
  }
}

/**
 * 把表单值序列化为提交给后端的 notify_cooldowns JSON：
 * 只序列化非空且为非负整数的键，全空返回 ""（不覆盖）。
 * 非数字 / 负数 / 小数输入直接跳过该键（后端会拒绝，前端先挡一道）。
 */
export function buildNotifyCooldowns(v: CooldownFormValues): string {
  const out: Record<string, number> = {}
  for (const key of Object.keys(v) as (keyof CooldownFormValues)[]) {
    const s = v[key].trim()
    if (s === "") continue
    const n = Number(s)
    if (Number.isFinite(n) && Number.isInteger(n) && n >= 0) {
      out[key] = n
    }
  }
  return Object.keys(out).length > 0 ? JSON.stringify(out) : ""
}
