"use client"

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { useRateCompare } from "@/lib/queries"
import { channelTypeLabel, formatRatio, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"

/**
 * 跨渠道比价双视图（docs/specs/rate-compare.md）。
 *   - 视图 ① 整体倍率排行：每渠道最低分组倍率，全渠道可比，零配置；
 *   - 视图 ② 同名分组比价：归一化后字面同名的分组内对比，最优渠道高亮。
 * 分组名跨站没有可靠自动对齐，自造命名的分组不会出现在视图 ②。
 */
export function RateCompare() {
  const { data, loading } = useRateCompare()
  const channels = data?.channels ?? []
  const groups = data?.groups ?? []

  return (
    <Card className="border border-border shadow-none">
      <CardHeader className="pb-2">
        <CardTitle className="text-base font-semibold">{"跨渠道比价"}</CardTitle>
      </CardHeader>
      <CardContent className="px-6">
        {loading ? (
          <p className="py-6 text-xs text-muted-foreground">{"加载中…"}</p>
        ) : (
          <section className="space-y-5">
            <div>
              <h3 className="text-xs font-medium text-muted-foreground">{"整体倍率排行"}</h3>
              {channels.length === 0 ? (
                <p className="py-3 text-xs text-muted-foreground">{"暂无倍率数据"}</p>
              ) : (
                <ul className="mt-1 divide-y divide-border">
                  {channels.map((r, i) => (
                    <li key={r.channel_id} className="flex items-center justify-between gap-3 py-2.5">
                      <div className="flex min-w-0 items-center gap-2">
                        <span
                          className={cn(
                            "shrink-0 text-sm font-semibold tabular-nums",
                            i === 0 ? "text-success" : "text-foreground",
                          )}
                        >
                          {formatRatio(r.min_ratio)}
                        </span>
                        <span className="truncate text-sm text-foreground">{r.channel_name}</span>
                        <span className="shrink-0 text-[10px] text-muted-foreground">
                          {channelTypeLabel(r.channel_type)}
                        </span>
                      </div>
                      <div className="flex shrink-0 items-center gap-2 text-xs text-muted-foreground">
                        <span className="text-muted-foreground">{"起价"}</span>
                        <span>{r.min_group}</span>
                        <span className="text-muted-foreground">{"·"}</span>
                        <span>{`${r.group_count} 个分组`}</span>
                        <span className="text-muted-foreground">{"·"}</span>
                        <span>{relativeTime(r.last_seen_at)}</span>
                      </div>
                    </li>
                  ))}
                </ul>
              )}
            </div>

            <div>
              <h3 className="text-xs font-medium text-muted-foreground">{"同名分组比价"}</h3>
              {groups.length === 0 ? (
                <p className="py-3 text-xs text-muted-foreground">
                  {"暂无可比分组（同名分组需出现在 ≥2 个渠道）"}
                </p>
              ) : (
                <ul className="mt-1 space-y-4">
                  {groups.map((g) => (
                    <li key={g.model_name}>
                      <div className="flex items-center justify-between gap-3">
                        <div className="flex min-w-0 items-baseline gap-2">
                          <span className="truncate text-sm font-semibold text-foreground">
                            {g.model_name}
                          </span>
                          {g.description ? (
                            <span className="truncate text-xs text-muted-foreground">
                              {g.description}
                            </span>
                          ) : null}
                        </div>
                        <div className="flex shrink-0 items-baseline gap-1 text-xs text-muted-foreground">
                          <span>{"价差"}</span>
                          <span className="font-medium tabular-nums">{`${g.spread_pct.toFixed(1)}%`}</span>
                        </div>
                      </div>
                      <ul className="mt-1.5 divide-y divide-border">
                        {g.entries.map((e) => {
                          const best = e.channel_id === g.best_channel_id
                          return (
                            <li
                              key={e.channel_id}
                              className="flex items-center justify-between gap-3 py-2"
                            >
                              <div className="flex min-w-0 items-center gap-2">
                                <span className="truncate text-sm text-foreground">
                                  {e.channel_name}
                                </span>
                                <span className="shrink-0 text-[10px] text-muted-foreground">
                                  {channelTypeLabel(e.channel_type)}
                                </span>
                                {best ? (
                                  <span className="shrink-0 rounded-md bg-success/10 px-1.5 py-0.5 text-[10px] font-medium text-success">
                                    {"最优"}
                                  </span>
                                ) : null}
                              </div>
                              <div className="flex shrink-0 items-center gap-2 text-xs text-muted-foreground">
                                <span
                                  className={cn(
                                    "font-semibold tabular-nums",
                                    best ? "text-success" : "text-foreground",
                                  )}
                                >
                                  {formatRatio(e.ratio)}
                                </span>
                                <span>{relativeTime(e.last_seen_at)}</span>
                              </div>
                            </li>
                          )
                        })}
                      </ul>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </section>
        )}
      </CardContent>
    </Card>
  )
}
