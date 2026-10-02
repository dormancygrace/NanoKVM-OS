package router

import (
	"context"
	"fmt"
	"time"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/config"
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
		updateReply(c, gin.H{"installed": gin.H{"version": osupdate.InstalledVersion()}, "apk": osupdate.GetAPKStatus(),
			"alpine": gin.H{"enabled": config.GetInstance().Alpine.BuilderURL != "", "current": osupdate.GetAlpineCurrent(), "operation": osupdate.GetAlpineState()}}, nil)
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
	api.GET("/alpine", func(c *gin.Context) {
		updateReply(c, gin.H{"enabled": config.GetInstance().Alpine.BuilderURL != "", "current": osupdate.GetAlpineCurrent(), "operation": osupdate.GetAlpineState()}, nil)
	})
	api.POST("/alpine/build", func(c *gin.Context) {
		var req osupdate.AlpineBuildRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			updateReply(c, nil, err)
			return
		}
		lock, err := osupdate.Lock()
		if err != nil {
			updateReply(c, nil, err)
			return
		}
		if err = osupdate.SetAlpineState(osupdate.AlpineState{State: "building", Profile: req.Profile, Packages: req.Packages, Message: "Building Alpine image"}); err != nil {
			lock.Close()
			updateReply(c, nil, err)
			return
		}
		builder := config.GetInstance().Alpine.BuilderURL
		go func() {
			defer lock.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			state, buildErr := osupdate.BuildAlpine(ctx, builder, req)
			if buildErr != nil {
				_ = osupdate.SetAlpineState(osupdate.AlpineState{State: "failed", Profile: req.Profile, Packages: req.Packages, Message: buildErr.Error()})
				return
			}
			_ = osupdate.SetAlpineState(state)
		}()
		updateReply(c, gin.H{"state": "building"}, nil)
	})
	api.POST("/alpine/stage", func(c *gin.Context) {
		lock, err := osupdate.Lock()
		if err != nil {
			updateReply(c, nil, err)
			return
		}
		state := osupdate.GetAlpineState()
		if state.State != "built" || state.BuildID == "" {
			lock.Close()
			updateReply(c, nil, fmt.Errorf("build an Alpine image first"))
			return
		}
		state.State, state.Message = "staging", "Downloading and verifying Alpine image"
		if err = osupdate.SetAlpineState(state); err != nil {
			lock.Close()
			updateReply(c, nil, err)
			return
		}
		builder := config.GetInstance().Alpine.BuilderURL
		go func() {
			defer lock.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()
			if stageErr := osupdate.StageAlpine(ctx, builder, state); stageErr != nil {
				state.State, state.Message = "failed", stageErr.Error()
				_ = osupdate.SetAlpineState(state)
				return
			}
			state.State, state.Message = "staged", "Alpine image verified and staged; installation still requires confirmation"
			_ = osupdate.SetAlpineState(state)
		}()
		updateReply(c, gin.H{"state": "staging"}, nil)
	})
	api.POST("/alpine/install", func(c *gin.Context) {
		lock, err := osupdate.Lock()
		if err != nil {
			updateReply(c, nil, err)
			return
		}
		state := osupdate.GetAlpineState()
		if state.State != "staged" {
			lock.Close()
			updateReply(c, nil, fmt.Errorf("stage an Alpine image first"))
			return
		}
		state.State, state.Message = "installing", "Activating Alpine recovery image and rebooting"
		if err = osupdate.SetAlpineState(state); err != nil {
			lock.Close()
			updateReply(c, nil, err)
			return
		}
		go func() {
			defer lock.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			if installErr := osupdate.ActivateAlpine(ctx); installErr != nil {
				state.State, state.Message = "failed", installErr.Error()
				_ = osupdate.SetAlpineState(state)
			}
		}()
		updateReply(c, gin.H{"state": "installing", "reboot": true}, nil)
	})
}
