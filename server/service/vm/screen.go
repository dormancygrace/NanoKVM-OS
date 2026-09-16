package vm

import (
	"NanoKVM-Server/common"
	"fmt"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/proto"
)

var screenFileMap = map[string]string{
	"type":       "/kvmapp/kvm/type",
	"fps":        "/kvmapp/kvm/fps",
	"quality":    "/kvmapp/kvm/qlty",
	"resolution": "/kvmapp/kvm/res",
}

func (s *Service) GetScreen(c *gin.Context) {
	current := common.GetScreen()
	portrait, portraitSupported := common.MonitorPortraitStatus()
	portraitResolution := common.PortraitResolution()
	portraitMaxSupported := common.PortraitMaxSupported()
	var rsp proto.Response
	rsp.OkRspWithData(c, gin.H{"width": current.Width, "height": current.Height, "fps": current.FPS,
		"quality": current.Quality, "bitRate": current.BitRate, "gop": current.GOP,
		"monitor":                     common.ReadVideoValue("/etc/kvm/monitor_resolution"),
		"monitorRequiresPowerCycle":   common.MonitorRequiresPowerCycle(),
		"monitorPowerCyclePending":    common.MonitorPowerCyclePending(),
		"monitorHighRefreshSupported": common.MonitorHighRefreshSupported(),
		"monitorSupported":            common.MonitorProfileSupported(), "qhdSupported": common.SupportsQHD(),
		"portrait": portrait, "portraitSupported": portraitSupported,
		"portraitResolution": portraitResolution, "portraitMaxSupported": portraitMaxSupported,
		"inputWidth":   common.ReadVideoValue("/run/nanokvm/width"),
		"inputHeight":  common.ReadVideoValue("/run/nanokvm/height"),
		"outputWidth":  common.ReadVideoValue("/run/nanokvm/stream_width"),
		"outputHeight": common.ReadVideoValue("/run/nanokvm/stream_height"),
		"measuredFps":  common.ReadVideoValue("/run/nanokvm/now_fps"),
		"effectiveFps": common.GetCaptureScreen().FPS})
}

func (s *Service) SetScreen(c *gin.Context) {
	var req proto.SetScreenReq
	var rsp proto.Response

	err := proto.ParseFormRequest(c, &req)
	if err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}

	switch req.Type {
	case "monitor_power_cycle_ack":
		if !req.ConfirmPowerCycle {
			rsp.ErrRsp(c, -1, "power cycle confirmation required")
			return
		}
		if err = common.ClearMonitorPowerCyclePending(); err != nil {
			rsp.ErrRsp(c, -4, err.Error())
			return
		}
		rsp.OkRsp(c)
		return
	case "portrait":
		if req.Value != 0 && req.Value != 1 {
			rsp.ErrRsp(c, -1, "portrait must be 0 or 1")
			return
		}
		if err = common.ApplyMonitorPortrait(req.Value == 1); err != nil {
			rsp.ErrRsp(c, -4, err.Error())
			return
		}
		rsp.OkRsp(c)
		return
	case "portrait_resolution":
		if req.Value != 2304 && req.Value != 1280 && req.Value != 1920 && req.Value != 2560 {
			rsp.ErrRsp(c, -1, "unsupported portrait monitor resolution")
			return
		}
		if req.Value == 2560 && !common.PortraitMaxSupported() {
			rsp.ErrRsp(c, -3, "maximum portrait monitor profile is unavailable")
			return
		}
		if (req.Value == 2304 || req.Value == 1280 || req.Value == 1920) && !common.PortraitSupported() {
			rsp.ErrRsp(c, -3, "portrait monitor profile is unavailable")
			return
		}
		if err = common.ApplyPortraitResolution(uint16(req.Value)); err != nil {
			rsp.ErrRsp(c, -4, err.Error())
			return
		}
		rsp.OkRsp(c)
		return
	case "monitor":
		if common.MonitorRequiresPowerCycle() && !req.ConfirmPowerCycle {
			rsp.ErrRsp(c, -5, "Physical power cycle required after EDID programming; confirm before writing")
			return
		}
		if req.Value != 0 && req.Value != 720 && req.Value != 1080 && req.Value != 1440 {
			rsp.ErrRsp(c, -1, "unsupported monitor profile")
			return
		}
		if req.Value == 1440 && !common.SupportsQHD() {
			rsp.ErrRsp(c, -3, "QHD requires at least 62 MiB of ION memory")
			return
		}
		if err = common.ApplyMonitorResolution(uint16(req.Value)); err != nil {
			rsp.ErrRsp(c, -4, err.Error())
			return
		}
		rsp.OkRsp(c)
		return
	case "resolution":
		if req.Value < 0 || req.Value > 1440 {
			rsp.ErrRsp(c, -1, "unsupported stream limit")
			return
		}
		if _, ok := common.ResolutionMap[uint16(req.Value)]; !ok {
			rsp.ErrRsp(c, -1, "unsupported stream limit")
			return
		}
		err = writeScreen(req.Type, strconv.Itoa(req.Value))
	case "fps":
		if req.Value < 10 || req.Value > 120 {
			rsp.ErrRsp(c, -1, "FPS must be between 10 and 120")
			return
		}
		err = writeScreen(req.Type, strconv.Itoa(req.Value))

	case "type":
		data := ""
		switch req.Value {
		case 0:
			data = "mjpeg"
		case 1:
			data = "h264"
		case 2:
			data = "h265"
		default:
			rsp.ErrRsp(c, -1, "stream type must be MJPEG, H.264, or H.265")
			return
		}
		err = writeScreen("type", data)

	case "gop":
		gop := 30
		if req.Value >= 1 && req.Value <= 100 {
			gop = req.Value
		}
		common.GetKvmVision().SetGop(uint8(gop))

	default:
		data := strconv.Itoa(req.Value)
		err = writeScreen(req.Type, data)
	}

	if err != nil {
		rsp.ErrRsp(c, -2, "update screen failed")
		return
	}

	common.SetScreen(req.Type, req.Value)

	log.Debugf("update screen: %+v", req)
	if req.Type == "fps" {
		rsp.OkRspWithData(c, gin.H{"fps": common.GetScreen().FPS})
		return
	}
	rsp.OkRsp(c)
}

func writeScreen(key string, value string) error {
	file, ok := screenFileMap[key]
	if !ok {
		return fmt.Errorf("invalid argument %s", key)
	}

	err := os.WriteFile(file, []byte(value), 0o666)
	if err != nil {
		log.Errorf("write kvm %s failed: %s", file, err)
		return err
	}

	return nil
}
