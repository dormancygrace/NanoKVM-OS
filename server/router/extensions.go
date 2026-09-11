package router

import (
	"NanoKVM-Server/authn"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/service/extensions/openvpn"
	"NanoKVM-Server/service/extensions/tailscale"
	"NanoKVM-Server/service/extensions/vpn"
	"NanoKVM-Server/service/extensions/wireguard"

	"github.com/gin-gonic/gin"
)

func extensionsRouter(r *gin.Engine) {
	api := r.Group("/api/extensions").Use(
		middleware.CheckToken(),
		middleware.RequireRole(authn.RoleAdmin),
	)

	api.GET("/vpn/versions", vpn.Versions)
	ovpn := openvpn.NewService()
	api.GET("/openvpn/status", ovpn.GetStatus)
	api.POST("/openvpn/import", ovpn.Import)
	api.POST("/openvpn/profile", ovpn.Change)
	wg := wireguard.NewService()
	api.GET("/wireguard/status", wg.GetStatus)
	api.POST("/wireguard/import", wg.Import)
	api.POST("/wireguard/profile", wg.Change)

	ts := tailscale.NewService()

	api.POST("/tailscale/install", ts.Install)     // install tailscale
	api.POST("/tailscale/uninstall", ts.Uninstall) // uninstall tailscale
	api.GET("/tailscale/status", ts.GetStatus)     // get tailscale status
	api.POST("/tailscale/up", ts.Up)               // run tailscale up
	api.POST("/tailscale/down", ts.Down)           // run tailscale down
	api.POST("/tailscale/login", ts.Login)         // tailscale login
	api.POST("/tailscale/logout", ts.Logout)       // tailscale logout
	api.POST("/tailscale/start", ts.Start)         // tailscale start
	api.POST("/tailscale/stop", ts.Stop)           // tailscale stop
	api.POST("/tailscale/restart", ts.Restart)     // tailscale restart
}
