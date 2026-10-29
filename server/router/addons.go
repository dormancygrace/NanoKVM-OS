package router

import (
	"context"
	"errors"
	"os"
	"time"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/osupdate"
	"NanoKVM-Server/pkg/bmc"
	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/extensions/apkpkg"
	"NanoKVM-Server/service/picoclaw"
	"NanoKVM-Server/service/vm"
	"github.com/gin-gonic/gin"
)

// Navigation needs only installation facts. It must not run package queries,
// probe a gateway, or wait for service status.
func addonsRouter(r *gin.Engine) {
	r.GET("/api/addons/inventory", middleware.CheckToken(), middleware.RequireRole(authn.RoleAdmin), func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		rustdesk, err := installedBinary("/usr/bin/nanokvm-rustdesk")
		var response proto.Response
		if err != nil {
			response.ErrRsp(c, -1, "cannot read installed extensions")
			return
		}
		ipmiInstalled, err := installedBinary(bmc.IPMIDaemonPath)
		if err != nil {
			response.ErrRsp(c, -1, "cannot read installed extensions")
			return
		}
		pico, err := picoclaw.Installed()
		if err != nil {
			response.ErrRsp(c, -1, "cannot read installed extensions")
			return
		}
		tpu, err := installedBinary(vm.ScreenTextHelper)
		if err != nil {
			response.ErrRsp(c, -1, "cannot read installed extensions")
			return
		}
		response.OkRspWithData(c, gin.H{
			"rustdesk": gin.H{"installed": rustdesk},
			"picoclaw": gin.H{"installed": pico},
			"ipmi":     gin.H{"installed": ipmiInstalled},
			"tpu":      gin.H{"installed": tpu, "enabled": vm.ScreenTextEnabled()},
		})
	})
}

// The add-ons of the NanoKVM repository, as of the last update check: the
// Add-ons page lists them, so a new add-on appears without an OS release.
func addonCatalogRouter(r *gin.Engine) {
	api := r.Group("/api/addons/catalog").Use(middleware.CheckToken(), middleware.RequireRole(authn.RoleAdmin))
	api.GET("", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		osupdate.EnsureAddonCatalog()
		updateReply(c, osupdate.GetAddonCatalog(), nil)
	})
	api.POST("/check", func(c *gin.Context) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			_ = osupdate.CheckUpdates(ctx, true)
		}()
		updateReply(c, nil, nil)
	})
	api.POST("/:action", func(c *gin.Context) {
		var req struct {
			Name string `json:"name"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Name == "" {
			updateReply(c, nil, errors.New("name is required"))
			return
		}
		var err error
		switch c.Param("action") {
		case "install":
			err = apkpkg.InstallAddon(req.Name)
		case "remove":
			err = apkpkg.RemoveAddon(req.Name)
		default:
			err = errors.New("invalid add-on action")
		}
		updateReply(c, osupdate.GetAddonCatalog(), err)
	})
}

func installedBinary(path string) (bool, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.Mode().IsRegular(), nil
}
