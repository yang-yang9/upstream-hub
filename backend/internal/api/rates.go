package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

func registerRates(g *gin.RouterGroup, d *Deps) {
	// 跨渠道比价双视图（docs/specs/rate-compare.md）。min_channels 缺省 2，非法值回退 2。
	g.GET("/rate-compare", func(c *gin.Context) {
		minChannels, _ := strconv.Atoi(c.DefaultQuery("min_channels", "2"))
		if minChannels < 1 {
			minChannels = 2
		}
		result, err := d.Monitor.RateCompare(minChannels)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": result})
	})

	g.GET("/rate-changes", func(c *gin.Context) {
		var channelID uint
		if s := c.Query("channel_id"); s != "" {
			id, err := strconv.ParseUint(s, 10, 64)
			if err != nil {
				fail(c, http.StatusBadRequest, err)
				return
			}
			channelID = uint(id)
		}
		limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
		list, err := d.Rates.ListChanges(channelID, limit)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": list})
	})
}
