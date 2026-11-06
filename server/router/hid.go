package router

import (
	"github.com/gin-gonic/gin"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/service/hid"
)

func hidRouter(r *gin.Engine) {
	service := hid.NewService()
	service.SetManualInputPolicy(allowManualInput)
	api := r.Group("/api").Use(middleware.CheckToken())

	api.POST("/hid/paste", requireInputOwner(), service.Paste)           // paste
	api.POST("/hid/sequence", requireInputOwner(), service.SendSequence) // Ctrl+Alt+Del, SysRq, REISUB
	api.GET("/hid/sequence", service.GetSequenceStatus)                  // the running or last sequence

	api.GET("/hid/shortcuts", service.GetShortcuts)           // get shortcuts
	api.GET("/hid/shortcut/leader-key", service.GetLeaderKey) // get shortcut leader key

	api.GET("/hid/leds", service.GetKeyboardLedStatus)

	admin := r.Group("/api").Use(
		middleware.CheckToken(),
		middleware.RequireRole(authn.RoleAdmin),
	)
	admin.POST("/hid/reset", service.ResetHid) // reset hid
	admin.POST("/hid/shortcut", service.AddShortcut)
	admin.DELETE("/hid/shortcut", service.DeleteShortcut)
	admin.POST("/hid/shortcut/leader-key", service.SetLeaderKey)
}
