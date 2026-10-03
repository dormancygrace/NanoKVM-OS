package router

import (
	"NanoKVM-Server/authn"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/picoclaw"
	"github.com/gin-gonic/gin"
	"os"
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
		pico, err := picoclaw.Installed()
		if err != nil {
			response.ErrRsp(c, -1, "cannot read installed extensions")
			return
		}
		response.OkRspWithData(c, gin.H{
			"rustdesk": gin.H{"installed": rustdesk},
			"picoclaw": gin.H{"installed": pico},
		})
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
