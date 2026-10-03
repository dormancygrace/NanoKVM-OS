package router

import (
	"NanoKVM-Server/authn"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/rustdesk"
	"context"
	"github.com/gin-gonic/gin"
	"net/http"
)

func rustdeskRouter(r *gin.Engine) {
	bridge := rustdesk.NewBridge()
	go bridge.Monitor(context.Background())
	service := rustdesk.NewService(bridge)
	api := r.Group("/api/addons/rustdesk").Use(middleware.CheckToken(), middleware.RequireRole(authn.RoleAdmin))
	api.GET("/source", func(c *gin.Context) {
		source := service.SourceURL()
		if source == "" {
			c.Status(http.StatusNotFound)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Redirect(http.StatusTemporaryRedirect, source)
	})
	api.GET("/status", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		data, err := service.Status()
		var response proto.Response
		if err != nil {
			response.ErrRsp(c, -1, err.Error())
			return
		}
		response.OkRspWithData(c, data)
	})
	api.PUT("/config", func(c *gin.Context) {
		var request rustdesk.Config
		var response proto.Response
		if err := c.ShouldBindJSON(&request); err != nil {
			response.ErrRsp(c, -1, "invalid configuration")
			return
		}
		if err := service.Configure(request); err != nil {
			response.ErrRsp(c, -1, err.Error())
			return
		}
		response.OkRsp(c)
	})
	api.POST("/:action", func(c *gin.Context) {
		var response proto.Response
		if err := service.Action(c.Param("action")); err != nil {
			response.ErrRsp(c, -1, err.Error())
			return
		}
		response.OkRsp(c)
	})
}
