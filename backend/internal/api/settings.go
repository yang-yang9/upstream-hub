// /api/settings 运行时配置热更新端点（P3-14，docs/specs/runtime-settings.md）。
//
// GET    当前生效值（app_settings 行 > env 推导基线）+ saved 标记；
// PUT    全量 17 字段替换（UI 语义：无部分更新），校验失败 400；
// DELETE 删行回退 env / config.yaml。
package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/worryzyy/upstream-hub/internal/settings"
	"github.com/worryzyy/upstream-hub/internal/storage"
)

// settingsView AppSettings 的 17 个 json 字段 + saved 状态标记。
// 内嵌平铺：storage.AppSettings 的 json tag 直接暴露在响应顶层。
type settingsView struct {
	storage.AppSettings
	Saved bool `json:"saved"`
}

func viewOf(s storage.AppSettings, saved bool) settingsView {
	return settingsView{AppSettings: s, Saved: saved}
}

func registerSettings(api *gin.RouterGroup, d *Deps) {
	if d.Settings == nil {
		return
	}
	api.GET("/settings", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"data": viewOf(d.Settings.Current(), d.Settings.Saved())})
	})
	api.PUT("/settings", func(c *gin.Context) {
		var v storage.AppSettings
		if err := c.ShouldBindJSON(&v); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		// 单行表语义：ID / 时间戳不接受请求体指定（UpdatedAt 由 gorm 落库时维护）。
		v.ID = 0
		v.UpdatedAt = time.Time{}
		if err := d.Settings.Update(v); err != nil {
			var ve *settings.ValidationError
			if errors.As(err, &ve) {
				fail(c, http.StatusBadRequest, err)
				return
			}
			fail(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": viewOf(d.Settings.Current(), d.Settings.Saved())})
	})
	api.DELETE("/settings", func(c *gin.Context) {
		if err := d.Settings.Reset(); err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": viewOf(d.Settings.Current(), d.Settings.Saved())})
	})
}
