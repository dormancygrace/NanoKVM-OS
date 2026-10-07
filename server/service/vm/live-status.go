package vm

import (
	"github.com/gin-gonic/gin"

	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/hid"
	"NanoKVM-Server/service/stream/audio"
)

// liveStatus combines the states the remote desktop page watches, so one
// request replaces five separate polls. Each field has the payload of its own
// endpoint; a part that cannot be read is null and the others still arrive.
type liveStatus struct {
	HDMI  *proto.GetGetHdmiStateRsp  `json:"hdmi"`
	GPIO  *proto.GetGpioRsp          `json:"gpio"`
	Input gin.H                      `json:"input"`
	Audio gin.H                      `json:"audio"`
	USB   *proto.GetVirtualDeviceRsp `json:"usb"` // administrators only
}

// GetLiveStatus serves GET /api/vm/live-status. The USB composition is
// included only for administrators, like GET /api/vm/device/virtual.
func (s *Service) GetLiveStatus(c *gin.Context, admin bool) {
	status := liveStatus{HDMI: hdmiState(), Input: hid.InputStatus(), Audio: audio.StatusData()}
	if leds, err := monitoredGPIOInputs.Current(c.Request.Context()); err == nil {
		status.GPIO = &proto.GetGpioRsp{PWR: leds.Power, HDD: leds.HDD}
	}
	if admin {
		status.USB = virtualDeviceState()
	}
	var rsp proto.Response
	rsp.OkRspWithData(c, status)
}
