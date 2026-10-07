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
		"monitorSupported":            common.MonitorProfileSupported(), "qhdSupported": common.SupportsQHD(), "uhdSupported": common.SupportsUHD(),
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
		"frameDetect":       common.FrameDetectEnabled(),
		"effectiveFps":      common.GetCaptureScreen().FPS})
}

func (s *Service) SetScreen(c *gin.Context) {
	if principal, ok := middleware.CurrentPrincipal(c); !ok || principal.Role != authn.RoleAdmin {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}

	var req proto.SetScreenReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}
	if failure := validateScreenSetting(req); failure != nil {
		rsp.ErrRsp(c, failure.code, failure.msg)
		return
	}
	data, failure := applyScreenSetting(req)
	if failure != nil {
		rsp.ErrRsp(c, failure.code, failure.msg)
		return
	}
	if data == nil {
		rsp.OkRsp(c)
		return
	}
	rsp.OkRspWithData(c, data)
}

type settingError struct {
	code int
	msg  string
}

func settingFailure(code int, msg string) *settingError { return &settingError{code, msg} }

// validateScreenSetting checks a setting without changing anything, so a
// batch can reject a request before applying any part of it.
func validateScreenSetting(req proto.SetScreenReq) *settingError {
	switch req.Type {
	case "mjpeg_chroma":
		if req.Value != 420 && req.Value != 422 {
			return settingFailure(-1, "MJPEG chroma must be 420 or 422")
		}
	case "monitor_power_cycle_ack":
		if !req.ConfirmPowerCycle {
			return settingFailure(-1, "power cycle confirmation required")
		}
	case "portrait":
		if req.Value != 0 && req.Value != 1 {
			return settingFailure(-1, "portrait must be 0 or 1")
		}
	case "portrait_resolution":
		if req.Value != 2304 && req.Value != 1280 && req.Value != 1920 && req.Value != 2560 {
			return settingFailure(-1, "unsupported portrait monitor resolution")
		}
		if req.Value == 2560 && !common.PortraitMaxSupported() {
			return settingFailure(-3, "maximum portrait monitor profile is unavailable")
		}
		if req.Value != 2560 && !common.PortraitSupported() {
			return settingFailure(-3, "portrait monitor profile is unavailable")
		}
	case "monitor":
		if common.MonitorRequiresPowerCycle() && !req.ConfirmPowerCycle {
			return settingFailure(-5, "Physical power cycle required after EDID programming; confirm before writing")
		}
		if req.Value != 0 && req.Value != 720 && req.Value != 1080 && req.Value != 1440 && req.Value != 2160 {
			return settingFailure(-1, "unsupported monitor profile")
		}
		if req.Value == 1440 && !common.SupportsQHD() {
			return settingFailure(-3, "QHD requires at least 62 MiB of ION memory")
		}
		if req.Value == 2160 && !common.SupportsUHD() {
			return settingFailure(-3, "3840x2160 requires the 128 MiB CMA video memory")
		}
	case "resolution":
		if req.Value < 0 || req.Value > 2160 || (req.Value > 1440 && !common.SupportsUHD()) {
			return settingFailure(-1, "unsupported stream limit")
		}
		if _, ok := common.ResolutionMap[uint16(req.Value)]; !ok {
			return settingFailure(-1, "unsupported stream limit")
		}
	case "fps":
		if req.Value < 10 || req.Value > 120 {
			return settingFailure(-1, "FPS must be between 10 and 120")
		}
	case "type":
		if req.Value < 0 || req.Value > 2 {
			return settingFailure(-1, "stream type must be MJPEG, H.264, or H.265")
		}
	case "gop":
	case "gop_mode":
		if req.Value != int(common.GOPModeNormalP) && req.Value != int(common.GOPModeSmartP) {
			return settingFailure(-1, "GOP mode must be NormalP or SmartP")
		}
	case "quality":
		if req.Value < 1 || req.Value > maxQualityValue {
			return settingFailure(-1, "quality must be 1-100, or a bitrate up to 20000 kbit/s")
		}
	default:
		return settingFailure(-1, "unknown screen setting")
	}
	return nil
}

// applyScreenSetting applies a validated setting and returns the response
// data of the single-setting endpoint (nil for a plain success).
func applyScreenSetting(req proto.SetScreenReq) (gin.H, *settingError) {
	var err error
	switch req.Type {
	case "mjpeg_chroma":
		mjpegChromaMutex.Lock()
		defer mjpegChromaMutex.Unlock()
		previous := common.GetScreen().MjpegChroma
		if applyMjpegChroma(uint16(req.Value)) != 0 {
			return nil, settingFailure(-4, "cannot apply MJPEG chroma")
		}
		if err = writeScreen(req.Type, strconv.Itoa(req.Value)); err != nil {
			applyMjpegChroma(previous)
			return nil, settingFailure(-2, "update screen failed")
		}
		common.SetScreen(req.Type, req.Value)
		active, reason := readMjpegChromaStatus()
		return gin.H{"mjpegChroma": req.Value, "mjpegChromaActive": active, "mjpegChromaFallback": reason}, nil
	case "monitor_power_cycle_ack":
		if err = common.ClearMonitorPowerCyclePending(); err != nil {
			return nil, settingFailure(-4, err.Error())
		}
		return nil, nil
	case "portrait":
		if err = common.ApplyMonitorPortrait(req.Value == 1); err != nil {
			return nil, settingFailure(-4, err.Error())
		}
		return nil, nil
	case "portrait_resolution":
		if err = common.ApplyPortraitResolution(uint16(req.Value)); err != nil {
			return nil, settingFailure(-4, err.Error())
		}
		return nil, nil
	case "monitor":
		if err = common.ApplyMonitorResolution(uint16(req.Value)); err != nil {
			return nil, settingFailure(-4, err.Error())
		}
		return nil, nil
	case "resolution", "fps", "gop_mode", "quality":
		err = writeScreen(req.Type, strconv.Itoa(req.Value))
	case "type":
		err = writeScreen("type", []string{"mjpeg", "h264", "h265"}[req.Value])
	case "gop":
		gop := 30
		if req.Value >= 1 && req.Value <= 100 {
			gop = req.Value
		}
		common.GetKvmVision().SetGop(uint8(gop))
		// Store the value the encoder actually uses, not the raw request.
		req.Value = gop
	}
	if err != nil {
		return nil, settingFailure(-2, "update screen failed")
	}

	common.SetScreen(req.Type, req.Value)

	log.Debugf("update screen: %+v", req)
	switch req.Type {
	case "fps":
		return gin.H{"fps": common.GetScreen().FPS}, nil
	case "gop_mode":
		selected := common.GetScreen().GOPMode
		active := readActiveGOPMode()
		return gin.H{
			"gopMode": selected, "gopModeActive": active,
			"gopModeRestartRequired": selected != active,
		}, nil
	}
	return nil, nil
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
