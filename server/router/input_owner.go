package router

import (
	"github.com/gin-gonic/gin"

	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/controlmode"
	"NanoKVM-Server/service/picoclaw"
	"NanoKVM-Server/service/ws"
)

// inputOwnerRequiredCode is returned when a request that acts on the managed
// host does not come from the browser tab that holds input control.
const inputOwnerRequiredCode = -4

// InputLeaseHeader carries the input lease the controlling WebSocket received
// with its control status.
const InputLeaseHeader = "X-NanoKVM-Input-Lease"

// requireInputOwner applies the single-controller rule of the WebSocket input
// path to HTTP routes that act on the managed host (paste, ATX power/reset).
// The lease belongs to one socket, so another tab, another login of the same
// user or a view-only viewer cannot satisfy it. With no controller at all
// (for example API automation without an open browser) requests proceed.
func requireInputOwner() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !ws.GetManager().AllowsInputLease(c.GetHeader(InputLeaseHeader)) {
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
