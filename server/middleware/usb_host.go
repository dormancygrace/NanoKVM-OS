package middleware

import (
	"net/http"

	"NanoKVM-Server/usbinternet"
	"NanoKVM-Server/utils"

	"github.com/gin-gonic/gin"
)

// USBRequest is one request the managed computer may make over the USB
// network.
type USBRequest struct{ Method, Path string }

// USBHostGate keeps the managed computer out of the web interface. The usb0
// input filter (usbinternet) leaves the web port open for the few requests
// the USB features need: on a connection that arrived over usb0 only those
// are served, unless the computer was given access to everything
// (usb.host_access). The interface is the one the kernel saw, not the
// destination address, which the computer can choose.
//
// A connection whose interface cannot be read is served: the kernel records
// it for every TCP connection, so this is no path the computer controls.
func USBHostGate(allowed ...USBRequest) gin.HandlerFunc {
	return func(c *gin.Context) {
		if ingress, err := utils.IngressInterface(c.Request); err != nil || ingress != "usb0" ||
			usbHostAllowed(allowed, c.Request.Method, c.Request.URL.Path) ||
			usbinternet.DefaultRuntime().HostAccess() {
			c.Next()
			return
		}
		c.AbortWithStatus(http.StatusForbidden)
	}
}

func usbHostAllowed(allowed []USBRequest, method, path string) bool {
	for _, a := range allowed {
		if a.Method == method && a.Path == path {
			return true
		}
	}
	return false
}
