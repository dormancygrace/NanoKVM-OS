package vm

import (
	"NanoKVM-Server/common"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/proto"
)

var screenFileMap = map[string]string{
	"type":         "/kvmapp/kvm/type",
	"fps":          "/kvmapp/kvm/fps",
	"gop_mode":     "/kvmapp/kvm/gop_mode",
	"mjpeg_chroma": "/kvmapp/kvm/mjpeg_chroma",
	"quality":      "/kvmapp/kvm/qlty",
	"resolution":   "/kvmapp/kvm/res",
}

var readActiveGOPMode = common.GetActiveGOPMode
var mjpegChromaMutex sync.Mutex
var readMjpegChromaStatus = common.GetMjpegChromaStatus
var applyMjpegChroma = func(value uint16) int { return common.GetKvmVision().SetMjpegChroma(value) }

// monitorScreenTypes reprogram the HDMI receiver's EDID flash or clear its
// power-cycle marker; they are device configuration, not shared viewing.
var monitorScreenTypes = map[string]bool{
	"monitor":                 true,
	"monitor_power_cycle_ack": true,
	"portrait":                true,
	"portrait_resolution":     true,
}

// maxQualityValue covers MJPEG quality (1-100) and H.26x bitrate in kbit/s.
// The native readers of /kvmapp/kvm/qlty use small fixed buffers.
const maxQualityValue = common.MaxQualityValue

func (s *Service) GetScreen(c *gin.Context) {
	current := common.GetScreen()
	activeGOPMode := readActiveGOPMode()
	activeChroma, chromaFallback := readMjpegChromaStatus()
	portrait, portraitSupported := common.MonitorPortraitStatus()
	portraitResolution := common.PortraitResolution()
	portraitMaxSupported := common.PortraitMaxSupported()
	var rsp proto.Response
	rsp.OkRspWithData(c, gin.H{"width": current.Width, "height": current.Height, "fps": current.FPS,
		"quality": current.Quality, "bitRate": current.BitRate, "gop": current.GOP,
		"mjpegChroma": current.MjpegChroma, "mjpegChromaActive": activeChroma, "mjpegChromaFallback": chromaFallback,
		"gopMode": current.GOPMode, "gopModeActive": activeGOPMode,
		"gopModeRestartRequired":      current.GOPMode != activeGOPMode,
		"monitor":                     common.ReadVideoValue("/etc/kvm/monitor_resolution"),
		"monitorRequiresPowerCycle":   common.MonitorRequiresPowerCycle(),
		"monitorPowerCyclePending":    common.MonitorPowerCyclePending(),
		"monitorHighRefreshSupported": common.MonitorHighRefreshSupported(),
		"monitorSupported":            common.MonitorProfileSupported(), "qhdSupported": common.SupportsQHD(),
		"portrait": portrait, "portraitSupported": portraitSupported,
		"portraitResolution": portraitResolution, "portraitMaxSupported": portraitMaxSupported,
		"inputWidth":        common.ReadVideoValue("/run/nanokvm/width"),
		"inputHeight":       common.ReadVideoValue("/run/nanokvm/height"),
		"outputWidth":       common.ReadVideoValue("/run/nanokvm/stream_width"),
		"outputHeight":      common.ReadVideoValue("/run/nanokvm/stream_height"),
		"mjpegOutputWidth":  common.ReadVideoValue("/run/nanokvm/mjpeg_width"),
		"mjpegOutputHeight": common.ReadVideoValue("/run/nanokvm/mjpeg_height"),
		"videoOutputWidth":  common.ReadVideoValue("/run/nanokvm/video_width"),
		"videoOutputHeight": common.ReadVideoValue("/run/nanokvm/video_height"),
		"measuredFps":       common.ReadVideoValue("/run/nanokvm/now_fps"),
		"effectiveFps":      common.GetCaptureScreen().FPS})
}

func (s *Service) SetScreen(c *gin.Context) {
	var req proto.SetScreenReq
	var rsp proto.Response

	err := proto.ParseFormRequest(c, &req)
	if err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}

	if monitorScreenTypes[req.Type] {
		if principal, ok := middleware.CurrentPrincipal(c); !ok || principal.Role != authn.RoleAdmin {
			c.JSON(http.StatusForbidden, "forbidden")
			return
		}
	}

	switch req.Type {
	case "mjpeg_chroma":
		mjpegChromaMutex.Lock()
		defer mjpegChromaMutex.Unlock()
		if req.Value != 420 && req.Value != 422 {
			rsp.ErrRsp(c, -1, "MJPEG chroma must be 420 or 422")
			return
		}
		previous := common.GetScreen().MjpegChroma
		if applyMjpegChroma(uint16(req.Value)) != 0 {
			rsp.ErrRsp(c, -4, "cannot apply MJPEG chroma")
			return
		}
		if err = writeScreen(req.Type, strconv.Itoa(req.Value)); err != nil {
			applyMjpegChroma(previous)
			rsp.ErrRsp(c, -2, "update screen failed")
			return
		}
		common.SetScreen(req.Type, req.Value)
		active, reason := readMjpegChromaStatus()
		rsp.OkRspWithData(c, gin.H{"mjpegChroma": req.Value, "mjpegChromaActive": active, "mjpegChromaFallback": reason})
		return
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
		// Store the value the encoder actually uses, not the raw request.
		req.Value = gop

	case "gop_mode":
		if req.Value != int(common.GOPModeNormalP) && req.Value != int(common.GOPModeSmartP) {
			rsp.ErrRsp(c, -1, "GOP mode must be NormalP or SmartP")
			return
		}
		err = writeScreen(req.Type, strconv.Itoa(req.Value))

	case "quality":
		if req.Value < 1 || req.Value > maxQualityValue {
			rsp.ErrRsp(c, -1, "quality must be 1-100, or a bitrate up to 20000 kbit/s")
			return
		}
		err = writeScreen(req.Type, strconv.Itoa(req.Value))

	default:
		rsp.ErrRsp(c, -1, "unknown screen setting")
		return
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
	if req.Type == "gop_mode" {
		selected := common.GetScreen().GOPMode
		active := readActiveGOPMode()
		rsp.OkRspWithData(c, gin.H{
			"gopMode": selected, "gopModeActive": active,
			"gopModeRestartRequired": selected != active,
		})
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
