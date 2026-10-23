package router

import (
	"github.com/gin-gonic/gin"

	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/controlmode"
	"NanoKVM-Server/service/hid"
	"NanoKVM-Server/service/picoclaw"
	"NanoKVM-Server/service/ws"
)

// inputOwnerRequiredCode is returned when a request that acts on the managed
// host does not come from the browser tab that holds input control.
const inputOwnerRequiredCode = hid.InputOwnerCode

// InputLeaseHeader carries the input lease the controlling WebSocket received
// with its control status.
const InputLeaseHeader = "X-NanoKVM-Input-Lease"

// requireInputOwner applies the single-controller rule of the WebSocket input
// path to HTTP routes that act on the managed host (paste, ATX power/reset).
// The lease belongs to one socket, so another tab, another login of the same
// user or a view-only viewer cannot satisfy it. With no controller at all
// (for example API automation without an open browser) requests proceed.
// The check is repeated before every key press of a paste or sequence: one
// admitted without a lease stops too once a browser that connects becomes
// the controller, or an add-on takes the input.
func requireInputOwner() gin.HandlerFunc {
	return func(c *gin.Context) {
		lease := c.GetHeader(InputLeaseHeader)
		if !ws.GetManager().AllowsInputLease(lease) {
			var rsp proto.Response
			rsp.ErrRsp(c, inputOwnerRequiredCode, "another session holds input control")
			c.Abort()
			return
		}
		// Asked again by long requests: a paste must stop at a takeover.
		hid.SetInputOwner(c, func() bool { return ws.GetManager().AllowsInputLease(lease) })
		c.Next()
	}
}

// allowManualInput mirrors ws.Client: manual input yields to an active
// PicoClaw session.
func allowManualInput(mode controlmode.Mode) bool {
	return mode != controlmode.ModePicoclaw || !picoclaw.GetSessionLock().BlocksManualInput()
}
