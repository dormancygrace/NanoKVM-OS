package vm

import (
	"encoding/binary"
	"net/http"
	"os"
	"sync"

	"github.com/gin-gonic/gin"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/common"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/proto"
)

// Reasons are stable codes; the web UI translates them.
const (
	reasonVideoMemory = "video-memory"
	reasonReceiver    = "receiver"
)

type monitorModeCapability struct {
	Height    uint16 `json:"height"`
	Width     uint16 `json:"width"`
	Rates     []int  `json:"rates"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

type portraitCapability struct {
	Resolution uint16   `json:"resolution"`
	Width      uint16   `json:"width"`
	Rate       int      `json:"rate"`
	Codecs     []string `json:"codecs"`
	Transports []string `json:"transports"`
	Available  bool     `json:"available"`
	Reason     string   `json:"reason,omitempty"`
}

type streamLimitCapability struct {
	Height    uint16 `json:"height"`
	Width     uint16 `json:"width"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// GetVideoCapabilities describes every video choice this device offers and
// why the others are unavailable, so clients need no hardware tables.
func (s *Service) GetVideoCapabilities(c *gin.Context) {
	var rsp proto.Response
	rsp.OkRspWithData(c, videoCapabilities())
}

func videoCapabilities() gin.H {
	qhd, uhd := common.SupportsQHD(), common.SupportsUHD()
	programmable := common.MonitorProfileSupported()
	live := common.MonitorHighRefreshSupported()
	portrait, portraitSupported := common.MonitorPortraitStatus()
	portraitMax := common.PortraitMaxSupported()

	mode := func(height uint16, needsLive bool, memoryOK bool) monitorModeCapability {
		m := monitorModeCapability{Height: height, Width: common.ResolutionMap[height],
			Rates: common.MonitorRates(height), Available: programmable}
		if m.Rates == nil {
			m.Rates = []int{}
		}
		switch {
		case !programmable || (needsLive && !live):
			m.Available, m.Reason = false, reasonReceiver
		case !memoryOK:
			m.Available, m.Reason = false, reasonVideoMemory
		}
		return m
	}
	monitorModes := []monitorModeCapability{
		mode(0, false, true),
		mode(2160, true, uhd),
		mode(1440, true, qhd),
		mode(1080, false, true),
		mode(720, false, true),
	}

	portraitProfile := func(resolution, width uint16, rate int, codecs, transports []string, ok bool) portraitCapability {
		p := portraitCapability{Resolution: resolution, Width: width, Rate: rate,
			Codecs: codecs, Transports: transports, Available: ok}
		if !ok {
			p.Reason = reasonReceiver
			if live && programmable {
				p.Reason = reasonVideoMemory
			}
		}
		return p
	}
	all := []string{"mjpeg", "h264", "h265"}
	allTransports := []string{"direct", "webrtc", "mjpeg"}
	portraitProfiles := []portraitCapability{
		portraitProfile(1280, 720, 120, all, allTransports, portraitSupported),
		portraitProfile(1920, 1080, 75, all, allTransports, portraitSupported),
		// H.264 tops out at 2304 lines; the tallest profile is H.265 Direct.
		portraitProfile(2304, 1296, 50, []string{"h264"}, []string{"direct", "webrtc"}, portraitSupported),
		portraitProfile(2560, 1440, 50, []string{"h265"}, []string{"direct"}, portraitMax),
	}

	limit := func(height uint16, memoryOK bool) streamLimitCapability {
		l := streamLimitCapability{Height: height, Width: common.ResolutionMap[height], Available: memoryOK}
		if !memoryOK {
			l.Reason = reasonVideoMemory
		}
		return l
	}
	streamLimits := []streamLimitCapability{
		limit(0, true), limit(2160, uhd), limit(1440, qhd), limit(1080, true), limit(720, true), limit(600, true),
	}

	inputWidth := common.ReadVideoValue("/run/nanokvm/width")
	inputHeight := common.ReadVideoValue("/run/nanokvm/height")
	return gin.H{
		"input": gin.H{
			"width": inputWidth, "height": inputHeight,
			"fps":    common.ReadVideoValue("/run/nanokvm/input_fps"),
			"maxFps": common.CaptureRateLimit(inputWidth, inputHeight),
		},
		"monitor": gin.H{
			"programmable":       programmable,
			"requiresPowerCycle": common.MonitorRequiresPowerCycle(),
			"powerCyclePending":  common.MonitorPowerCyclePending(),
			"followsStreamRate":  common.MonitorRefreshFollowsStream(),
			"selected":           common.ReadVideoValue("/etc/kvm/monitor_resolution"),
			"refreshHz":          common.MonitorRefreshHz(),
			"modes":              monitorModes,
			"portrait": gin.H{
				"enabled": portrait, "resolution": common.PortraitResolution(),
				"profiles": portraitProfiles,
			},
		},
		"stream": gin.H{
			"limits":    streamLimits,
			"rateTiers": common.CaptureRateTiers,
			"minFps":    10,
			"maxFps":    120,
		},
		// Every transport carries every codec; the browser checks its decoder.
		"transports": gin.H{
			"direct": []string{"h264", "h265"},
			"webrtc": []string{"h264", "h265"},
			"mjpeg":  []string{"mjpeg"},
		},
		"videoMemoryMiB": videoMemoryMiB(),
	}
}

func videoMemoryMiB() int {
	data, err := os.ReadFile("/proc/device-tree/reserved-memory/ion/size")
	if err != nil || len(data) != 4 {
		return 0
	}
	return int(binary.BigEndian.Uint32(data) >> 20)
}

// VideoSettingsReq is a set of screen settings applied together; absent
// fields stay unchanged.
type VideoSettingsReq struct {
	ConfirmPowerCycle  bool  `json:"confirmPowerCycle"`
	Type               *int  `json:"type"`
	Quality            *int  `json:"quality"`
	BitRate            *int  `json:"bitRate"`
	GOP                *int  `json:"gop"`
	GOPMode            *int  `json:"gopMode"`
	MjpegChroma        *int  `json:"mjpegChroma"`
	Height             *int  `json:"height"`
	FPS                *int  `json:"fps"`
	PortraitResolution *int  `json:"portraitResolution"`
	Portrait           *bool `json:"portrait"`
	Monitor            *int  `json:"monitor"`
}

// settings lists the request in application order. The EDID is written last
// and at most once, after the stream rate it follows has been saved.
func (r VideoSettingsReq) settings() []proto.SetScreenReq {
	var list []proto.SetScreenReq
	add := func(name string, value *int) {
		if value != nil {
			list = append(list, proto.SetScreenReq{Type: name, Value: *value, ConfirmPowerCycle: r.ConfirmPowerCycle})
		}
	}
	add("type", r.Type)
	add("quality", r.Quality)
	add("quality", r.BitRate)
	add("gop", r.GOP)
	add("gop_mode", r.GOPMode)
	add("mjpeg_chroma", r.MjpegChroma)
	add("resolution", r.Height)
	add("fps", r.FPS)
	add("portrait_resolution", r.PortraitResolution)
	if r.Portrait != nil {
		value := 0
		if *r.Portrait {
			value = 1
		}
		add("portrait", &value)
	}
	add("monitor", r.Monitor)
	return list
}

var videoSettingsMutex sync.Mutex

// SetVideoSettings validates every setting before it changes any, applies
// them in order and, when the stream rate changed but the monitor did not,
// rewrites the EDID so the monitor refresh follows the stream rate.
func (s *Service) SetVideoSettings(c *gin.Context) {
	if principal, ok := middleware.CurrentPrincipal(c); !ok || principal.Role != authn.RoleAdmin {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	var req VideoSettingsReq
	var rsp proto.Response
	if err := c.ShouldBindJSON(&req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}
	if req.Quality != nil && (*req.Quality < 1 || *req.Quality > 100) {
		rsp.ErrRsp(c, -1, "MJPEG quality must be 1-100")
		return
	}
	if req.BitRate != nil && (*req.BitRate <= 100 || *req.BitRate > maxQualityValue) {
		rsp.ErrRsp(c, -1, "bitrate must be 101-20000 kbit/s")
		return
	}
	videoSettingsMutex.Lock()
	defer videoSettingsMutex.Unlock()

	settings := req.settings()
	for _, setting := range settings {
		if failure := validateScreenSetting(setting); failure != nil {
			rsp.ErrRsp(c, failure.code, failure.msg)
			return
		}
	}
	monitorWritten := false
	for _, setting := range settings {
		if _, failure := applyScreenSetting(setting); failure != nil {
			rsp.ErrRsp(c, failure.code, failure.msg)
			return
		}
		monitorWritten = monitorWritten || setting.Type == "monitor" || setting.Type == "portrait" ||
			setting.Type == "portrait_resolution"
	}
	if req.FPS != nil && !monitorWritten {
		if _, err := common.SyncMonitorRefresh(common.GetScreen().FPS); err != nil {
			rsp.ErrRsp(c, -4, err.Error())
			return
		}
	}
	rsp.OkRspWithData(c, videoCapabilities())
}
