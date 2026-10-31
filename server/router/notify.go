package router

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/notify"
	"NanoKVM-Server/service/vm"
)

func notifyRouter(r *gin.Engine) {
	service := notify.Default()
	vm.StartNotifyWatch()
	vm.StartScreenWatch()

	api := r.Group("/api/notify").Use(middleware.CheckToken(), middleware.RequireRole(authn.RoleAdmin))
	api.GET("/config", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		var response proto.Response
		response.OkRspWithData(c, service.Status())
	})
	api.PUT("/config", func(c *gin.Context) {
		var request notify.Settings
		var response proto.Response
		if err := c.ShouldBindJSON(&request); err != nil {
			response.ErrRsp(c, -1, "invalid configuration")
			return
		}
		if err := service.Configure(request); err != nil {
			response.ErrRsp(c, -1, err.Error())
			return
		}
		response.OkRspWithData(c, service.Status())
	})
	// Sends a test message through one channel as it is on the page.
	api.POST("/test", func(c *gin.Context) {
		var request notify.ChannelUpdate
		var response proto.Response
		if err := c.ShouldBindJSON(&request); err != nil {
			response.ErrRsp(c, -1, "invalid channel")
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()
		if err := service.TestChannel(ctx, request); err != nil {
			response.ErrRsp(c, -2, err.Error())
			return
		}
		response.OkRsp(c)
	})
}
