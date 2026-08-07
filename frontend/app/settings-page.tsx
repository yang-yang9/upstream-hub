"use client"

import { useState } from "react"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { useAuth } from "@/lib/auth-context"
import { useUpgradeCheck } from "@/lib/queries"
import { apiFetch } from "@/lib/api"
import { toast } from "sonner"

export default function SettingsPage() {
  const { username } = useAuth()
  const { data: upgrade, loading, refetch } = useUpgradeCheck()
  const [upgrading, setUpgrading] = useState(false)
  const [upgraded, setUpgraded] = useState(false)
  const [restarting, setRestarting] = useState(false)
  const [showChangelog, setShowChangelog] = useState(false)

  async function handleUpgrade() {
    setUpgrading(true)
    try {
      await apiFetch("/upgrade/perform", { method: "POST" })
      toast.success("升级完成，请点击重启生效")
      setUpgraded(true)
    } catch (e) {
      toast.error((e as Error).message || "升级失败")
    } finally {
      setUpgrading(false)
    }
  }

  async function handleRestart() {
    setRestarting(true)
    try {
      await apiFetch("/upgrade/restart", { method: "POST" })
      toast.success("正在重启，页面将在几秒后刷新…")
      setTimeout(() => window.location.reload(), 5000)
    } catch {
      toast.error("重启请求失败")
      setRestarting(false)
    }
  }

  return (
    <section className="space-y-3">
      <header>
        <h1 className="text-lg font-semibold text-foreground">{"系统设置"}</h1>
        <p className="text-xs text-muted-foreground">
          {"账户信息与运行参数。修改 cron / 数据库 / 加密密钥需要重启后端。"}
        </p>
      </header>

      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        <Card className="border border-border shadow-none">
          <CardHeader className="pb-2">
            <CardTitle className="text-base font-semibold">{"当前账户"}</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">{"管理员"}</span>
              <span className="font-medium">{username ?? "—"}</span>
            </div>
            <p className="text-[11px] text-muted-foreground">
              {"账号 / 密码写在 backend/config.yaml 的 auth 段，或 ADMIN_USERNAME / ADMIN_PASSWORD 环境变量。"}
            </p>
          </CardContent>
        </Card>

        <Card className="border border-border shadow-none">
          <CardHeader className="pb-2">
            <CardTitle className="text-base font-semibold">{"调度计划"}</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">{"余额扫描"}</span>
              <span className="font-medium">{"每 15 分钟"}</span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">{"倍率扫描"}</span>
              <span className="font-medium">{"每 30 分钟"}</span>
            </div>
            <p className="text-[11px] text-muted-foreground">
              {"修改 backend/config.yaml 的 scheduler 段，重启后端生效。"}
            </p>
          </CardContent>
        </Card>

        <Card className="border border-border shadow-none">
          <CardHeader className="pb-2">
            <CardTitle className="text-base font-semibold">{"前端轮询"}</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">{"自动刷新"}</span>
              <span className="font-medium">{"30 秒"}</span>
            </div>
            <p className="text-[11px] text-muted-foreground">
              {"页面在后台标签时暂停轮询，回到前台立即触发一次。手动点头部的[刷新]立即拉。"}
            </p>
          </CardContent>
        </Card>

        <Card className="border border-border shadow-none">
          <CardHeader className="pb-2">
            <div className="flex items-center justify-between">
              <CardTitle className="text-base font-semibold">{"系统升级"}</CardTitle>
              {upgrade?.has_update && !upgraded && (
                <span className="rounded-full bg-primary/10 px-2 py-0.5 text-[11px] font-medium text-primary">
                  {"有新版本"}
                </span>
              )}
            </div>
          </CardHeader>
          <CardContent className="space-y-3 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">{"当前版本"}</span>
              <span className="font-medium font-mono text-xs">
                {loading ? "…" : (upgrade?.current_version ?? "—")}
              </span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">{"最新版本"}</span>
              <span className="font-medium font-mono text-xs">
                {loading ? "…" : (upgrade?.latest_version ?? "—")}
              </span>
            </div>

            {upgrade?.has_update && upgrade.changelog && (
              <div>
                <button
                  type="button"
                  className="text-[11px] text-primary hover:underline"
                  onClick={() => setShowChangelog(!showChangelog)}
                >
                  {showChangelog ? "收起更新日志" : "查看更新日志"}
                </button>
                {showChangelog && (
                  <pre className="mt-1.5 max-h-40 overflow-auto rounded bg-muted/50 p-2 text-[11px] text-muted-foreground whitespace-pre-wrap">
                    {upgrade.changelog}
                  </pre>
                )}
              </div>
            )}

            <div className="flex items-center gap-2 pt-1">
              {!upgraded && !upgrade?.has_update && (
                <Button
                  variant="outline"
                  size="sm"
                  className="h-7 text-xs"
                  onClick={refetch}
                  disabled={loading}
                >
                  {"检查更新"}
                </Button>
              )}
              {upgrade?.has_update && !upgraded && (
                <Button
                  size="sm"
                  className="h-7 text-xs"
                  onClick={handleUpgrade}
                  disabled={upgrading}
                >
                  {upgrading ? "升级中…" : "升级"}
                </Button>
              )}
              {upgraded && (
                <Button
                  size="sm"
                  className="h-7 text-xs"
                  onClick={handleRestart}
                  disabled={restarting}
                >
                  {restarting ? "重启中…" : "重启生效"}
                </Button>
              )}
            </div>

            {!upgrade?.has_update && !loading && (
              <p className="text-[11px] text-muted-foreground">{"已是最新版本。"}</p>
            )}
          </CardContent>
        </Card>
      </div>
    </section>
  )
}
