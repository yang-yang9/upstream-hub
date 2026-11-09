/**
 * 运行时配置卡片（P3-14，docs/specs/runtime-settings.md AC-6）：
 * 调度 / 通知 / 预测 三组 17 字段，保存后即时生效（PUT /api/settings），
 * 恢复默认删覆盖行回退 env / config.yaml（DELETE /api/settings）。
 */
"use client"

import { useEffect, useState } from "react"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Button } from "@/components/ui/button"
import { Switch } from "@/components/ui/switch"
import { Badge } from "@/components/ui/badge"
import { apiFetch } from "@/lib/api"
import { useRuntimeSettings } from "@/lib/queries"
import type { RuntimeSettings } from "@/lib/api-types"
import { toast } from "sonner"

/** 表单态：文本/数值字段一律存字符串，开关存布尔。 */
interface FormState {
  balance_cron: string
  rate_cron: string
  retention_cron: string
  retention_monitor_logs_days: string
  retention_balance_snapshots_days: string
  retention_notification_logs_days: string
  batch_rate_changes: boolean
  min_change_pct: string
  balance_low_cooldown_minutes: string
  failure_threshold: string
  failure_cooldown_minutes: string
  quiet_hours: string
  send_max_attempts: string
  balance_recovery: boolean
  forecast_window_days: string
  forecast_alert_days: string
  forecast_cooldown_minutes: string
}

function fromSettings(s: RuntimeSettings): FormState {
  return {
    balance_cron: s.balance_cron,
    rate_cron: s.rate_cron,
    retention_cron: s.retention_cron,
    retention_monitor_logs_days: String(s.retention_monitor_logs_days),
    retention_balance_snapshots_days: String(s.retention_balance_snapshots_days),
    retention_notification_logs_days: String(s.retention_notification_logs_days),
    batch_rate_changes: s.batch_rate_changes,
    min_change_pct: String(s.min_change_pct),
    balance_low_cooldown_minutes: String(s.balance_low_cooldown_minutes),
    failure_threshold: String(s.failure_threshold),
    failure_cooldown_minutes: String(s.failure_cooldown_minutes),
    quiet_hours: s.quiet_hours,
    send_max_attempts: String(s.send_max_attempts),
    balance_recovery: s.balance_recovery,
    forecast_window_days: String(s.forecast_window_days),
    forecast_alert_days: String(s.forecast_alert_days),
    forecast_cooldown_minutes: String(s.forecast_cooldown_minutes),
  }
}

/** 客户端预检（后端仍会校验）：cron 需 6 段（含秒）。 */
function cronError(f: FormState): string | null {
  for (const [label, expr] of [
    ["余额扫描 cron", f.balance_cron],
    ["倍率扫描 cron", f.rate_cron],
    ["清理任务 cron", f.retention_cron],
  ] as const) {
    if (expr.trim() === "") continue // 空 = 不调度该任务
    if (expr.trim().split(/\s+/).length !== 6) {
      return `${label} 需为 6 段（含秒）表达式，如 "0 */15 * * * *"`
    }
  }
  return null
}

interface NumField {
  key: keyof FormState
  label: string
  min: number
  float?: boolean
}

const SCHEDULE_NUMS: NumField[] = [
  { key: "retention_monitor_logs_days", label: "监控日志保留（天）", min: 0 },
  { key: "retention_balance_snapshots_days", label: "余额快照保留（天）", min: 0 },
  { key: "retention_notification_logs_days", label: "通知日志保留（天）", min: 0 },
]

const NOTIFY_NUMS: NumField[] = [
  { key: "min_change_pct", label: "最小变化幅度（%）", min: 0, float: true },
  { key: "balance_low_cooldown_minutes", label: "低余额冷却（分钟）", min: 0 },
  { key: "failure_threshold", label: "失败阈值（次）", min: 1 },
  { key: "failure_cooldown_minutes", label: "失败冷却（分钟）", min: 0 },
  { key: "send_max_attempts", label: "发送重试次数", min: 1 },
]

const FORECAST_NUMS: NumField[] = [
  { key: "forecast_window_days", label: "预测窗口（天）", min: 1 },
  { key: "forecast_alert_days", label: "余额耗尽预警（天）", min: 0 },
  { key: "forecast_cooldown_minutes", label: "预测冷却（分钟）", min: 0 },
]

export function RuntimeSettingsCard() {
  const { data, loading, error, refetch } = useRuntimeSettings()
  const [form, setForm] = useState<FormState>(() => fromSettings({
    balance_cron: "", rate_cron: "", retention_cron: "",
    retention_monitor_logs_days: 0, retention_balance_snapshots_days: 0,
    retention_notification_logs_days: 0, batch_rate_changes: true,
    min_change_pct: 0, balance_low_cooldown_minutes: 0, failure_threshold: 1,
    failure_cooldown_minutes: 0, quiet_hours: "", send_max_attempts: 1,
    balance_recovery: false, forecast_window_days: 1, forecast_alert_days: 0,
    forecast_cooldown_minutes: 0, saved: false, updated_at: null,
  }))
  const [validation, setValidation] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [resetting, setResetting] = useState(false)

  // GET 到数据后预填（只在数据到达时做一次映射，避免覆盖用户编辑）。
  useEffect(() => {
    if (data) setForm(fromSettings(data))
  }, [data])

  function set(key: keyof FormState, value: string | boolean) {
    setForm((f) => ({ ...f, [key]: value }))
  }

  async function handleSave() {
    const msg = cronError(form)
    if (msg) {
      setValidation(msg)
      return
    }
    setValidation(null)
    setSaving(true)
    try {
      await apiFetch("/api/settings", {
        method: "PUT",
        body: JSON.stringify({
          balance_cron: form.balance_cron,
          rate_cron: form.rate_cron,
          retention_cron: form.retention_cron,
          retention_monitor_logs_days: Number(form.retention_monitor_logs_days),
          retention_balance_snapshots_days: Number(form.retention_balance_snapshots_days),
          retention_notification_logs_days: Number(form.retention_notification_logs_days),
          batch_rate_changes: form.batch_rate_changes,
          min_change_pct: Number(form.min_change_pct),
          balance_low_cooldown_minutes: Number(form.balance_low_cooldown_minutes),
          failure_threshold: Number(form.failure_threshold),
          failure_cooldown_minutes: Number(form.failure_cooldown_minutes),
          quiet_hours: form.quiet_hours,
          send_max_attempts: Number(form.send_max_attempts),
          balance_recovery: form.balance_recovery,
          forecast_window_days: Number(form.forecast_window_days),
          forecast_alert_days: Number(form.forecast_alert_days),
          forecast_cooldown_minutes: Number(form.forecast_cooldown_minutes),
        }),
      })
      toast.success("已保存，已即时生效（无需重启）")
      refetch()
    } catch (e) {
      toast.error((e as Error).message || "保存失败")
    } finally {
      setSaving(false)
    }
  }

  async function handleReset() {
    setResetting(true)
    try {
      await apiFetch("/api/settings", { method: "DELETE" })
      toast.success("已恢复默认（回到启动配置）")
      refetch()
    } catch (e) {
      toast.error((e as Error).message || "恢复默认失败")
    } finally {
      setResetting(false)
    }
  }

  function numField(f: NumField) {
    return (
      <div key={f.key} className="space-y-1">
        <Label htmlFor={f.key} className="text-xs text-muted-foreground">{f.label}</Label>
        <Input
          id={f.key}
          inputMode={f.float ? "decimal" : "numeric"}
          className="h-8 text-xs"
          value={String(form[f.key])}
          onChange={(e) => set(f.key, e.target.value)}
        />
      </div>
    )
  }

  function textField(key: keyof FormState, label: string, placeholder: string) {
    return (
      <div key={key} className="space-y-1">
        <Label htmlFor={key} className="text-xs text-muted-foreground">{label}</Label>
        <Input
          id={key}
          className="h-8 text-xs font-mono"
          placeholder={placeholder}
          value={String(form[key])}
          onChange={(e) => set(key, e.target.value)}
        />
      </div>
    )
  }

  function switchField(key: "batch_rate_changes" | "balance_recovery", label: string, hint: string) {
    return (
      <div key={key} className="flex items-center justify-between gap-2">
        <div className="space-y-0.5">
          <Label htmlFor={key} className="text-xs">{label}</Label>
          <p className="text-[10px] text-muted-foreground">{hint}</p>
        </div>
        <Switch
          id={key}
          checked={form[key]}
          onCheckedChange={(v) => set(key, v)}
        />
      </div>
    )
  }

  return (
    <Card className="border border-border shadow-none">
      <CardHeader className="pb-2">
        <div className="flex items-center justify-between">
          <CardTitle className="text-base font-semibold">{"运行参数（热更新）"}</CardTitle>
          {data && (
            data.saved ? (
              <Badge variant="secondary" className="text-[10px]">已保存（优先于启动配置）</Badge>
            ) : (
              <Badge variant="outline" className="text-[10px]">未保存过</Badge>
            )
          )}
        </div>
        <p className="text-[11px] text-muted-foreground">
          {"保存后即时生效，无需重启；恢复默认 = 删除覆盖，回到 env / config.yaml。"}
        </p>
      </CardHeader>
      <CardContent className="space-y-4 text-sm">
        {loading && !data && <p className="text-xs text-muted-foreground">{"加载中…"}</p>}
        {error && <p className="text-xs text-destructive">{error}</p>}

        <section className="space-y-2">
          <h4 className="text-xs font-semibold text-foreground">{"调度"}</h4>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {textField("balance_cron", "余额扫描 cron", "0 */15 * * * *")}
            {textField("rate_cron", "倍率扫描 cron", "0 */30 * * * *")}
            {textField("retention_cron", "清理任务 cron", "0 17 3 * * *")}
            {SCHEDULE_NUMS.map(numField)}
          </div>
          <p className="text-[10px] text-muted-foreground">
            {"cron 需写满 6 段；留空 = 关闭该任务。"}
          </p>
        </section>

        <section className="space-y-2">
          <h4 className="text-xs font-semibold text-foreground">{"通知"}</h4>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {NOTIFY_NUMS.map(numField)}
            {textField("quiet_hours", "静默时段", "23:00-07:00")}
            {switchField("batch_rate_changes", "倍率变化合并推送", "一次扫描的多条变化合并成一条")}
            {switchField("balance_recovery", "余额恢复通知", "余额从低位回升时通知")}
          </div>
        </section>

        <section className="space-y-2">
          <h4 className="text-xs font-semibold text-foreground">{"预测"}</h4>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {FORECAST_NUMS.map(numField)}
          </div>
        </section>

        {validation && (
          <p className="text-xs text-destructive" role="alert">{validation}</p>
        )}

        <div className="flex items-center gap-2 pt-1">
          <Button size="sm" className="h-7 text-xs" onClick={handleSave} disabled={saving}>
            {saving ? "保存中…" : "保存"}
          </Button>
          <Button variant="outline" size="sm" className="h-7 text-xs" onClick={handleReset} disabled={resetting}>
            {resetting ? "恢复中…" : "恢复默认"}
          </Button>
          {data?.updated_at && (
            <span className="text-[10px] text-muted-foreground">
              {`上次保存：${new Date(data.updated_at).toLocaleString()}`}
            </span>
          )}
        </div>
      </CardContent>
    </Card>
  )
}
