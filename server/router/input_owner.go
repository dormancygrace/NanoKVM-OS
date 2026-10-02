package router

import (
	"github.com/gin-gonic/gin"

	"NanoKVM-Server/middleware"
	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/controlmode"
	"NanoKVM-Server/service/picoclaw"
	"NanoKVM-Server/service/ws"
)

// inputOwnerRequiredCode is returned when a view-only session tries to act
// on the managed host while another session holds input control.
const inputOwnerRequiredCode = -4

// requireInputOwner applies the single-controller rule of the WebSocket input
// path to HTTP routes that act on the managed host (paste, ATX power/reset).
func requireInputOwner() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !ws.GetManager().AllowsSession(middleware.CurrentSessionID(c)) {
			var rsp proto.Response
			rsp.ErrRsp(c, inputOwnerRequiredCode, "another session holds input control")
			c.Abort()
			return
		}
		c.Next()
	}
}

// allowManualInput mirrors ws.Client: manual input yields to an active
// PicoClaw session.
func allowManualInput(mode controlmode.Mode) bool {
	return mode != controlmode.ModePicoclaw || !picoclaw.GetSessionLock().BlocksManualInput()
}
