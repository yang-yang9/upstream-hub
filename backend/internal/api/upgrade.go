package api

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
)

func registerUpgrade(g *gin.RouterGroup, d *Deps) {
	gp := g.Group("/upgrade")
	gp.GET("/check", func(c *gin.Context) { checkUpgrade(c, d) })
	gp.POST("/perform", func(c *gin.Context) { performUpgrade(c, d) })
	gp.POST("/restart", func(c *gin.Context) { restart(c, d) })
}

func checkUpgrade(c *gin.Context, d *Deps) {
	if d.Updater == nil {
		fail(c, http.StatusServiceUnavailable, fmt.Errorf("updater not configured"))
		return
	}

	info, err := d.Updater.CheckLatest(c.Request.Context())
	if err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": info})
}

func performUpgrade(c *gin.Context, d *Deps) {
	if d.Updater == nil {
		fail(c, http.StatusServiceUnavailable, fmt.Errorf("updater not configured"))
		return
	}

	if err := d.Updater.Perform(c.Request.Context()); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func restart(c *gin.Context, d *Deps) {
	c.JSON(http.StatusOK, gin.H{"ok": true})
	go func() {
		time.Sleep(500 * time.Millisecond)
		os.Exit(0)
	}()
}
