package vm

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/config"
	"NanoKVM-Server/internal/gpioio"
	"NanoKVM-Server/proto"
)

var atxPulse gpioio.Controller
var atxRead sync.Mutex

func (s *Service) SetGpio(c *gin.Context) {
	var req proto.SetGpioReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, fmt.Sprintf("invalid arguments: %s", err))
		return
	}

	device := ""
	conf := config.GetInstance().Hardware

	switch req.Type {
	case "power":
		device = conf.GPIOPower
	case "reset":
		device = conf.GPIOReset
	default:
		rsp.ErrRsp(c, -2, fmt.Sprintf("invalid power event: %s", req.Type))
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

	if err := writeGpio(c.Request.Context(), device, duration); err != nil {
		rsp.ErrRsp(c, -3, fmt.Sprintf("operation failed: %s", err))
		return
	}

	log.Debugf("gpio %s set successfully", device)
	rsp.OkRsp(c)
}

func (s *Service) GetGpio(c *gin.Context) {
	var rsp proto.Response

	conf := config.GetInstance().Hardware

	pwr, err := readGpio(conf.GPIOPowerLED)
	if err != nil {
		rsp.ErrRsp(c, -2, fmt.Sprintf("failed to read power led: %s", err))
		return
	}

	hdd := false
	if conf.Version == config.HWVersionAlpha {
		hdd, err = readGpio(conf.GPIOHDDLed)
		if err != nil {
			rsp.ErrRsp(c, -2, fmt.Sprintf("failed to read hdd led: %s", err))
			return
		}
	}

	data := &proto.GetGpioRsp{
		PWR: pwr,
		HDD: hdd,
	}
	rsp.OkRspWithData(c, data)
}

func writeGpio(ctx context.Context, device string, duration time.Duration) error {
	return atxPulse.Pulse(ctx, duration, func() (gpioio.Line, error) {
		return gpioio.Open(device, true)
	})
}

func readGpio(device string) (bool, error) {
	atxRead.Lock()
	defer atxRead.Unlock()
	line, err := gpioio.Open(device, false)
	if err != nil {
		return false, err
	}
	high, err := line.Get()
	err = errors.Join(err, line.Close())
	// The ATX input reports an illuminated host power/HDD LED at logic low.
	return !high, err
}
