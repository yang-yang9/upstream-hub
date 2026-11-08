// /metrics 端点：Prometheus text format 暴露（docs/specs/metrics-export.md）。
//
// 认证：Deps.MetricsToken 未配置时公开（行为同 /healthz）；配置后要求
// Authorization: Bearer（metrics.CheckBearer 恒定时间比较）。
// 计数：scrapes / scrapeErrors 是进程内 atomic 计数器，闭包持有、随 Register 生命周期存活。
package api

import (
	"net/http"
	"sync/atomic"

	"github.com/gin-gonic/gin"
	"github.com/worryzyy/upstream-hub/internal/metrics"
	"github.com/worryzyy/upstream-hub/internal/version"
)

func registerMetrics(r *gin.Engine, d *Deps) {
	var scrapes, scrapeErrors atomic.Uint64

	r.GET("/metrics", func(c *gin.Context) {
		if !metrics.CheckBearer(c.Request.Header.Get("Authorization"), d.MetricsToken) {
			// 401 不查库、不计数（spec AC-5）。
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		channels, err := d.Channels.List()
		if err != nil {
			scrapeErrors.Add(1)
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		logs, err := d.MonLogs.LatestPerChannel()
		if err != nil {
			// 500 让 Prometheus 把 target 标 down（up=0），DB 故障的告警立即可见（spec AC-6）。
			scrapeErrors.Add(1)
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		scrapes.Add(1) // 成功渲染前自增：首次抓取即返回 1（spec AC-4）
		body := metrics.Render(metrics.Snapshot{
			Version:      version.Version,
			Commit:       version.Commit,
			Channels:     channels,
			LatestLogs:   logs,
			Scrapes:      scrapes.Load(),
			ScrapeErrors: scrapeErrors.Load(),
		})
		c.Data(http.StatusOK, "text/plain; version=0.0.4; charset=utf-8", []byte(body))
	})
}
