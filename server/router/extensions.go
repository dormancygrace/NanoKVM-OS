package router

import (
	"NanoKVM-Server/authn"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/service/extensions/acme"
	"NanoKVM-Server/service/extensions/netbird"
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
	api.POST("/openvpn/install", ovpn.Install)
	api.POST("/openvpn/uninstall", ovpn.Uninstall)
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

	nb := netbird.NewService()
	api.POST("/netbird/install", nb.Install)
	api.POST("/netbird/uninstall", nb.Uninstall)
	api.GET("/netbird/status", nb.GetStatus)
	api.POST("/netbird/login", nb.Login)
	api.POST("/netbird/down", nb.Down)
	api.POST("/netbird/start", nb.Start)
	api.POST("/netbird/stop", nb.Stop)
	api.POST("/netbird/restart", nb.Restart)

	ac := acme.NewService()
	api.GET("/acme/status", ac.GetStatus)       // acme.sh, settings, certificate, last run
	api.GET("/acme/providers", ac.GetProviders) // installed DNS hooks and their variables
	api.POST("/acme/install", ac.Install)       // install or update to the latest stable release
	api.POST("/acme/check", ac.CheckUpdate)     // ask GitHub for the latest stable release
	api.POST("/acme/uninstall", ac.Uninstall)   // remove acme.sh, account and certificate
	api.POST("/acme/issue", ac.Issue)           // issue (background job)
	api.POST("/acme/renew", ac.Renew)           // renew now (background job)
	api.POST("/acme/remove", ac.Remove)         // stop renewing, back to the generated pair

	// The HTTP-01 tokens, for the CA, on the HTTPS listener and in plain
	// HTTP mode; the redirect listener mounts the same handler.
	r.GET(acme.ChallengePrefix+":token", gin.WrapH(acme.ChallengeHandler()))
}
