package vm

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/internal/gpioio"
	"NanoKVM-Server/journal"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/proto"
)

var atxPulse gpioio.Controller

func (s *Service) SetGpio(c *gin.Context) {
	var req proto.SetGpioReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, fmt.Sprintf("invalid arguments: %s", err))
		return
	}

	device := ""
	conf := atxHardware()

	switch req.Type {
	case "power":
		device = conf.GPIOPower
	case "reset":
		device = conf.GPIOReset
	default:
		rsp.ErrRsp(c, -2, fmt.Sprintf("invalid power event: %s", req.Type))
		return
	}
	if device == "" {
		rsp.ErrRsp(c, -2, "ATX power control is off (System > Device)")
		return
	}

	if req.Duration > 60000 {
		rsp.ErrRsp(c, -1, "ATX duration must not exceed 60000 ms")
		return
	}
	var duration time.Duration
	if req.Duration > 0 {
		duration = time.Duration(req.Duration) * time.Millisecond
	} else {
		duration = 800 * time.Millisecond
	}

	ctx := c.Request.Context()
	if principal, ok := middleware.CurrentPrincipal(c); ok {
		ctx = journal.WithActor(ctx, principal.Username+" (web)")
	}
	if err := writeGpio(ctx, device, duration); err != nil {
		rsp.ErrRsp(c, -3, fmt.Sprintf("operation failed: %s", err))
		return
	}

	log.Debugf("gpio %s set successfully", device)
	rsp.OkRsp(c)
}

func (s *Service) GetGpio(c *gin.Context) {
	var rsp proto.Response

	if !ATXEnabled() {
		rsp.ErrRsp(c, -1, "ATX power control is off (System > Device)")
		return
	}
	status, err := gpioMonitor().Current(c.Request.Context())
	if err != nil {
		rsp.ErrRsp(c, -2, fmt.Sprintf("failed to read ATX LEDs: %s", err))
		return
	}

	data := &proto.GetGpioRsp{
		PWR: status.Power,
		HDD: status.HDD,
	}
	rsp.OkRspWithData(c, data)
}

// writeGpio presses a button of the host: every power action of the page,
// of Redfish, IPMI and MCP ends here, so here it gets into the journal,
// with the actor its request named.
func writeGpio(ctx context.Context, device string, duration time.Duration) error {
	err := atxPulse.Pulse(ctx, duration, func() (gpioio.Line, error) {
		return gpioio.Open(device, true)
	})
	if err == nil {
		button := "power"
		if device == atxHardware().GPIOReset {
			button = "reset"
		}
		journal.Action(ctx, "action.button", map[string]string{"button": button, "seconds": strconv.FormatFloat(duration.Seconds(), 'f', 1, 64)})
	}
	return err
}
