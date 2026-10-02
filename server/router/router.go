package router

import (
	"fmt"
	"os"
	"path/filepath"

	"NanoKVM-Server/middleware"
	"NanoKVM-Server/service/controlmode"
	"NanoKVM-Server/service/picoclaw"

	"github.com/gin-gonic/contrib/static"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// ownBodyLimitRoutes stream their uploads or set their own MaxBytesReader.
var ownBodyLimitRoutes = []string{
	"/api/download/file",
	"/api/branding/logo",
	"/api/branding/favicon",
	"/api/extensions/openvpn/import",
	"/api/extensions/wireguard/import",
	"/api/vm/script/upload",
	"/api/os/update/upload",
	picoclawBasePath + picoclawActionsPath,
	picoclawBasePath + picoclawMCPPath,
	picoclawBasePath + picoclawLoadImagePath,
}

func Init(r *gin.Engine) {
	r.Use(middleware.LimitRequestBody(ownBodyLimitRoutes...))
	web(r)
	server(r)
	log.Debugf("router init done")
}

func web(r *gin.Engine) {
	execPath, err := os.Executable()
	if err != nil {
		panic("invalid executable path")
	}

	execDir := filepath.Dir(execPath)
	webPath := fmt.Sprintf("%s/web", execDir)

	// The HTML entrypoint references content-hashed frontend assets. Prevent the
	// browser from caching index.html across a firmware replacement, otherwise it can keep
	// loading removed, older asset names (including outdated locale resources).
	r.Use(func(c *gin.Context) {
		if c.Request.URL.Path == "/" || c.Request.URL.Path == "/index.html" {
			c.Header("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
			c.Header("Pragma", "no-cache")
			c.Header("Expires", "0")
		}
		c.Next()
	})

	r.Use(static.Serve("/", static.LocalFile(webPath, true)))
}

func server(r *gin.Engine) {
	control := controlmode.GetManager()
	picoclawService := picoclaw.NewService(control)

	brandingRouter(r)
	authRouter(r)
	vmRouter(r)
	streamRouter(r)
	storageRouter(r)
	networkRouter(r)
	hidRouter(r)
	controlRouter(r, control, picoclawService)
	mcpRouter(r, control, picoclawService)
	picoclawRouter(r, picoclawService)
	wsRouter(r)
	downloadRouter(r)
	extensionsRouter(r)
	osUpdateRouter(r)
}

func LoopbackHTTPAllowedPaths() []string {
	paths := PicoclawLoopbackHTTPAllowedPaths()
	paths = append(paths, HIDLoopbackHTTPAllowedPaths()...)
	return paths
}
