package vm

import (
	"NanoKVM-Server/common"
	"fmt"
	"math"
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
	"mjpeg_chroma": "/kvmapp/kvm/mjpeg_chroma",
	"quality":      "/kvmapp/kvm/qlty",
	"resolution":   "/kvmapp/kvm/res",
	"hevc_direct":  common.HevcDirectSettingFile,
}

var screenMonitorRequiresPowerCycle = common.MonitorRequiresPowerCycle

var mjpegChromaMutex sync.Mutex
var readMjpegChromaStatus = common.GetMjpegChromaStatus
var applyMjpegChroma = func(value uint16) int { return common.GetKvmVision().SetMjpegChroma(value) }

// maxQualityValue covers MJPEG quality (1-100) and H.26x bitrate in kbit/s.
// The native readers of /kvmapp/kvm/qlty use small fixed buffers.
const maxQualityValue = common.MaxQualityValue

func (s *Service) GetScreen(c *gin.Context) {
	current := common.GetScreen()
	activeChroma, chromaFallback := readMjpegChromaStatus()
	portrait, portraitSupported := common.MonitorPortraitStatus()
	portraitResolution := common.PortraitResolution()
	portraitMaxSupported := common.PortraitMaxSupported()
	var rsp proto.Response
	rsp.OkRspWithData(c, gin.H{"width": current.Width, "height": current.Height, "fps": current.FPS,
		"quality": current.Quality, "bitRate": current.BitRate,
		"mjpegChroma": current.MjpegChroma, "mjpegChromaActive": activeChroma, "mjpegChromaFallback": chromaFallback,
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
		"measuredKbps":      common.ReadVideoValue("/run/nanokvm/now_kbps"),
		"frameDetect":       common.FrameDetectEnabled(),
		"hevcDirect":        common.HevcDirect(), "hevcDirectSupported": common.HevcDirectSupported(),
		"effectiveFps": common.GetCaptureScreen().FPS})
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
	// Serialize every setting with a batch.
	videoSettingsMutex.Lock()
	defer videoSettingsMutex.Unlock()
	if failure := validateScreenSetting(req); failure != nil {
		rsp.ErrRsp(c, failure.code, failure.msg)
		return
	}
	data, failure := applyScreenSetting(req)
	if failure != nil {
		rsp.ErrRsp(c, failure.code, failure.msg)
		return
	}
	if req.Type == "fps" {
		// The saved EDID refresh rate follows the stream rate. Without this, a
		// client of this endpoint leaves the source at the previous rate (a
		// 100 Hz source against a 60 fps stream judders). It reprograms the
		// EDID, so the HDMI link renegotiates and blanks briefly, but only when
		// the rate selects another profile.
		if err := applyVideoMonitorSettings(common.MonitorSettings{SyncRefresh: true}); err != nil {
			rsp.ErrRsp(c, -4, err.Error())
			return
		}
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
	return validateScreenSettingIn(req, nil)
}

// mjpegSelectionFailure refuses MJPEG while the HDMI input is 3840x2160. Inside
// a batch the monitor it also sets decides, as it changes the input.
func mjpegSelectionFailure(batch *VideoSettingsReq) *settingError {
	width, height := captureInput()
	if batch != nil && batch.Monitor != nil && *batch.Monitor > 0 && *batch.Monitor <= math.MaxUint16 {
		width, height = int(common.ResolutionMap[uint16(*batch.Monitor)]), *batch.Monitor
	}
	if !common.MjpegAllowedFor(width, height) {
		return settingFailure(-3, common.MjpegBlockedMessage)
	}
	return nil
}

// h264SelectionFailure refuses H.264 when the picture it would encode is
// larger than 2560x1440 (1080x1920 in portrait). Inside a batch the stream
// limit and the monitor it also sets decide, as they change the picture.
func h264SelectionFailure(batch *VideoSettingsReq) *settingError {
	limit := common.GetScreen().Height
	if batch != nil && batch.Height != nil && *batch.Height >= 0 && *batch.Height <= math.MaxUint16 {
		limit = uint16(*batch.Height)
	}
	if width, height, ok := batchMonitorInput(batch); ok {
		if !common.H264AllowedFor(width, height, limit) {
			return settingFailure(-3, common.H264BlockedMessage)
		}
		return nil
	}
	width, height := captureInput()
	if !common.H264SelectionAllowed(width, height, limit) {
		return settingFailure(-3, common.H264BlockedMessage)
	}
	return nil
}

// batchMonitorInput is the input a batch asks the source for: the portrait
// profile it turns on, else the landscape monitor it sets (Auto: the size it
// stands for); ok is false when the batch leaves the monitor as it is.
func batchMonitorInput(batch *VideoSettingsReq) (width, height int, ok bool) {
	if batch == nil {
		return 0, 0, false
	}
	if batch.Portrait != nil && *batch.Portrait {
		resolution := common.PortraitResolution()
		if batch.PortraitResolution != nil && *batch.PortraitResolution > 0 && *batch.PortraitResolution <= math.MaxUint16 {
			resolution = uint16(*batch.PortraitResolution)
		}
		return int(common.PortraitWidth(resolution)), int(resolution), true
	}
	if batch.Monitor == nil || *batch.Monitor < 0 || *batch.Monitor > math.MaxUint16 {
		return 0, 0, false
	}
	monitor := uint16(*batch.Monitor)
	if monitor == 0 {
		monitor = common.AutoMonitorHeight()
	}
	if monitor == 0 {
		return 0, 0, false
	}
	return int(common.ResolutionMap[monitor]), int(monitor), true
}

// validateScreenSettingIn is validateScreenSetting for a setting that is part
// of a batch (nil for a single one).
func validateScreenSettingIn(req proto.SetScreenReq, batch *VideoSettingsReq) *settingError {
	return validateScreenSettingFor(req, batch, common.BootedVideoPool())
}

// validateScreenSettingFor checks a setting against a video pool: the booted
// one, or the one of the mode a batch switches to.
func validateScreenSettingFor(req proto.SetScreenReq, batch *VideoSettingsReq, pool common.VideoPool) *settingError {
	// Portrait profiles also write receiver flash on Cube/Lite with an UXC.
	// Check before any batch setting can change stream state.
	if (req.Type == "monitor" || req.Type == "portrait" || req.Type == "portrait_resolution") &&
		screenMonitorRequiresPowerCycle() && !req.ConfirmPowerCycle {
		return settingFailure(-5, "Physical power cycle required after EDID programming; confirm before writing")
	}

	switch req.Type {
	case "mjpeg_chroma":
		if req.Value != 420 && req.Value != 422 {
			return settingFailure(-1, "MJPEG chroma must be 420 or 422")
		}
	case "hevc_direct":
		if req.Value != 0 && req.Value != 1 {
			return settingFailure(-1, "hevc_direct must be 0 or 1")
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
		if req.Value != 1280 && req.Value != 1920 && req.Value != 2560 {
			return settingFailure(-1, "unsupported portrait monitor resolution")
		}
		if req.Value == 2560 && !common.PortraitMaxSupportedFor(pool) {
			return settingFailure(-3, "maximum portrait monitor profile is unavailable")
		}
		if req.Value != 2560 && !common.PortraitSupportedFor(pool) {
			return settingFailure(-3, "portrait monitor profile is unavailable")
		}
	case "monitor":
		if req.Value != 0 && req.Value != 720 && req.Value != 1080 && req.Value != 1440 && req.Value != 2160 {
			return settingFailure(-1, "unsupported monitor profile")
		}
		if req.Value == 1440 && !pool.QHD() {
			return settingFailure(-3, "2560x1440 requires the QHD or UHD video memory mode (Settings > Memory)")
		}
		if req.Value == 2160 && !pool.UHD() {
			return settingFailure(-3, "3840x2160 requires the UHD video memory mode (Settings > Memory)")
		}
	case "resolution":
		if req.Value < 0 || req.Value > 2160 || (req.Value > 1440 && !pool.UHD()) {
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
		if req.Value == 0 {
			return mjpegSelectionFailure(batch)
		}
		if req.Value == 1 {
			return h264SelectionFailure(batch)
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
	case "hevc_direct":
		// libkvm follows the file within a second; nothing else holds it.
		if err = writeScreen(req.Type, strconv.Itoa(req.Value)); err != nil {
			return nil, settingFailure(-2, "update screen failed")
		}
		return gin.H{"hevcDirect": common.HevcDirect()}, nil
	case "resolution", "fps", "quality":
		err = writeScreen(req.Type, strconv.Itoa(req.Value))
	case "type":
		err = writeScreen("type", []string{"mjpeg", "h264", "h265"}[req.Value])
	}
	if err != nil {
		return nil, settingFailure(-2, "update screen failed")
	}

	common.SetScreen(req.Type, req.Value)

	log.Debugf("update screen: %+v", req)
	switch req.Type {
	case "fps":
		return gin.H{"fps": common.GetScreen().FPS}, nil
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
