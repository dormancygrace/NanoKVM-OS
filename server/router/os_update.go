package router

import (
	"context"
	"errors"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/osupdate"
	"NanoKVM-Server/proto"
	"github.com/gin-gonic/gin"
)

func updateReply(c *gin.Context, data any, err error) {
	var rsp proto.Response
	if err != nil {
		rsp.ErrRsp(c, -1, err.Error())
	} else {
		rsp.OkRspWithData(c, data)
	}
}

func osUpdateRouter(r *gin.Engine) {
	api := r.Group("/api/os/update").Use(middleware.CheckToken(), middleware.RequireRole(authn.RoleAdmin))
	api.GET("", func(c *gin.Context) {
		// Pages up to 3.1 read alpine.enabled without a check; this one keeps
		// such a page working while an update replaces it.
		updateReply(c, gin.H{"installed": gin.H{"version": osupdate.InstalledVersion()}, "apk": osupdate.GetAPKStatus(),
			"components": osupdate.GetComponents(), "notice": osupdate.GetNotice(),
			"alpine": gin.H{"enabled": false}}, nil)
	})
	// The menu polls this for its update badge: a file read, no apk run.
	api.GET("/notice", func(c *gin.Context) {
		updateReply(c, osupdate.GetNotice(), nil)
	})
	api.PUT("/notice", func(c *gin.Context) {
		var req struct {
			Enabled *bool `json:"enabled"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
			updateReply(c, nil, errors.New("enabled is required"))
			return
		}
		updateReply(c, nil, osupdate.SetNoticeEnabled(*req.Enabled))
	})
	api.POST("/notice/check", func(c *gin.Context) {
		go func() { _ = osupdate.CheckUpdates(context.Background(), true) }()
		updateReply(c, nil, nil)
	})
	api.POST("/apk/:action", func(c *gin.Context) {
		updateReply(c, nil, osupdate.StartAPK(c.Param("action")))
	})
	api.GET("/software", func(c *gin.Context) {
		installed, err := osupdate.ListInstalledSoftware()
		updateReply(c, gin.H{"installed": installed, "operation": osupdate.GetSoftwareStatus(), "indexes": osupdate.GetSoftwareIndexStatus()}, err)
	})
	api.GET("/software/status", func(c *gin.Context) {
		updateReply(c, gin.H{"operation": osupdate.GetSoftwareStatus(), "indexes": osupdate.GetSoftwareIndexStatus()}, nil)
	})
	api.GET("/software/index-schedule", func(c *gin.Context) {
		schedule, err := osupdate.GetIndexSchedule()
		updateReply(c, gin.H{"schedule": schedule, "timezone": osupdate.IndexScheduleTimezone()}, err)
	})
	api.PUT("/software/index-schedule", func(c *gin.Context) {
		var schedule osupdate.IndexSchedule
		if err := c.ShouldBindJSON(&schedule); err != nil {
			updateReply(c, nil, err)
			return
		}
		updateReply(c, nil, osupdate.SetIndexSchedule(schedule))
	})
	api.GET("/software/search", func(c *gin.Context) {
		packages, err := osupdate.SearchSoftware(c.Query("query"))
		updateReply(c, gin.H{"packages": packages}, err)
	})
	api.GET("/software/updates", func(c *gin.Context) {
		updates, err := osupdate.ListSoftwareUpdates()
		updateReply(c, gin.H{"updates": updates}, err)
	})
	api.POST("/software/remove/preview", func(c *gin.Context) {
		var req struct {
			Name string `json:"name"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			updateReply(c, nil, err)
			return
		}
		preview, err := osupdate.PreviewSoftwareRemoval(req.Name)
		updateReply(c, gin.H{"preview": preview}, err)
	})
	api.POST("/software/:action", func(c *gin.Context) {
		var req struct {
			Name string `json:"name"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			updateReply(c, nil, err)
			return
		}
		updateReply(c, nil, osupdate.StartSoftware(c.Param("action"), req.Name))
	})
}
