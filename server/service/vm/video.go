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
	reasonMjpeg4K     = "mjpeg-4k"
	reasonH2644K      = "h264-4k"
)

var captureInput = common.CaptureInputSize

type monitorModeCapability struct {
	Height uint16 `json:"height"`
	Width  uint16 `json:"width"`
	// Auto (height 0): the size it stands for; 0 for the stock profile.
	EffectiveWidth  uint16 `json:"effectiveWidth,omitempty"`
	EffectiveHeight uint16 `json:"effectiveHeight,omitempty"`
	Rates           []int  `json:"rates"`
	// The codecs this monitor can be encoded with at the Auto stream limit:
	// 3840x2160 needs H.265 (H.264 runs up to 2560x1440).
	Codecs    []string `json:"codecs"`
	Available bool     `json:"available"`
	Reason    string   `json:"reason,omitempty"`
}

type portraitCapability struct {
	Resolution uint16   `json:"resolution"`
	Width      uint16   `json:"width"`
	Rate       int      `json:"rate"`
	Rates      []int    `json:"rates"`
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

// mjpegCapability is MJPEG availability for the current capture; MaxSide is
// the longest input side it can run with.
type mjpegCapability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	MaxSide   int    `json:"maxSide"`
}

// h264Capability is H.264 availability for the current capture and stream
// limit; H.264 encodes pictures up to MaxLongSide wide and MaxHeight high.
type h264Capability struct {
	Available   bool   `json:"available"`
	Reason      string `json:"reason,omitempty"`
	MaxLongSide int    `json:"maxLongSide"`
	MaxHeight   int    `json:"maxHeight"`
}

func h264Capabilities(inputWidth, inputHeight int, streamHeight uint16) h264Capability {
	h := h264Capability{Available: true, MaxLongSide: common.H264MaxLongSide, MaxHeight: common.H264MaxHeight}
	if !common.H264AllowedFor(inputWidth, inputHeight, streamHeight) {
		h.Available, h.Reason = false, reasonH2644K
	}
	return h
}

// modeCodecs lists the codecs a monitor size can be encoded with at the Auto
// stream limit; an unknown size (the stock Auto profile) takes both.
func modeCodecs(width, height uint16) []string {
	if common.H264AllowedFor(int(width), int(height), 0) {
		return []string{"h264", "h265"}
	}
	return []string{"h265"}
}

func mjpegCapabilities(inputWidth, inputHeight int) mjpegCapability {
	m := mjpegCapability{Available: true, MaxSide: common.MjpegMaxLongSide}
	if !common.MjpegAllowedFor(inputWidth, inputHeight) {
		m.Available, m.Reason = false, reasonMjpeg4K
	}
	return m
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
		if height == 0 {
			m.EffectiveHeight = common.AutoMonitorHeight()
			m.EffectiveWidth = common.ResolutionMap[m.EffectiveHeight]
			m.Codecs = modeCodecs(m.EffectiveWidth, m.EffectiveHeight)
		} else {
			m.Codecs = modeCodecs(m.Width, m.Height)
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
			Rates: common.PortraitRates(resolution), Codecs: codecs, Transports: transports, Available: ok}
		if !ok {
			p.Reason = reasonReceiver
			// Only when the current boot image would capture it: a device
			// still on an earlier image gets it with the next restart.
			supported := common.PortraitSupportedFor(common.SharedVideoPool)
			if resolution == 2560 {
				supported = common.PortraitMaxSupportedFor(common.SharedVideoPool)
			}
			if live && programmable && supported {
				p.Reason = reasonVideoMemory
			}
		}
		return p
	}
	all := []string{"mjpeg", "h264", "h265"}
	allTransports := []string{"direct", "webrtc", "mjpeg"}
	portraitProfiles := []portraitCapability{
		portraitProfile(1280, 720, 120, all, allTransports, portraitSupported),
		portraitProfile(1920, 1080, 90, all, allTransports, portraitSupported),
		// H.264 encodes at most 2304 lines: 1296x2304 is the tallest portrait
		// for browsers without H.265, 1440x2560 needs H.265.
		portraitProfile(2304, 1296, 60, []string{"h264"}, []string{"direct", "webrtc"}, portraitSupported),
		portraitProfile(2560, 1440, 60, []string{"h265"}, []string{"direct", "webrtc"}, portraitMax),
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

	inputWidth, inputHeight := captureInput()
	return gin.H{
		"input": gin.H{
			"width": inputWidth, "height": inputHeight, "receiver": common.HDMIReceiverModel(),
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
			"mjpeg":     mjpegCapabilities(inputWidth, inputHeight),
			"h264":      h264Capabilities(inputWidth, inputHeight, common.GetScreen().Height),
			// The device transport: browsers without a choice of their own
			// start with it, and RustDesk follows it.
			"transport": StreamTransport(),
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
	MjpegChroma        *int  `json:"mjpegChroma"`
	HevcDirect         *int  `json:"hevcDirect"`
	Height             *int  `json:"height"`
	FPS                *int  `json:"fps"`
	PortraitResolution *int  `json:"portraitResolution"`
	Portrait           *bool `json:"portrait"`
	Monitor            *int  `json:"monitor"`
	// Transport is the device transport (direct, webrtc or mjpeg), stored
	// after the other settings.
	Transport *string `json:"transport"`
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
	add("mjpeg_chroma", r.MjpegChroma)
	add("hevc_direct", r.HevcDirect)
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

func (r VideoSettingsReq) storeTransport() error {
	if r.Transport == nil {
		return nil
	}
	return SetStreamTransport(*r.Transport)
}

// monitor is the final HDMI state of the request.
func (r VideoSettingsReq) monitor() common.MonitorSettings {
	monitor := common.MonitorSettings{Portrait: r.Portrait, SyncRefresh: r.FPS != nil}
	if r.Monitor != nil {
		value := uint16(*r.Monitor)
		monitor.Resolution = &value
	}
	if r.PortraitResolution != nil {
		value := uint16(*r.PortraitResolution)
		monitor.PortraitResolution = &value
	}
	return monitor
}

var videoSettingsMutex sync.Mutex

// Seams for tests.
var (
	applyVideoMonitorSettings = common.ApplyMonitorSettings
	bootedVideoPool           = common.BootedVideoPool
)

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
	if req.Transport != nil && !validTransport(*req.Transport) {
		rsp.ErrRsp(c, -1, "transport must be direct, webrtc or mjpeg")
		return
	}
	videoSettingsMutex.Lock()
	defer videoSettingsMutex.Unlock()

	pool := bootedVideoPool()
	settings := req.settings()
	for _, setting := range settings {
		if failure := validateScreenSettingFor(setting, &req, pool); failure != nil {
			rsp.ErrRsp(c, failure.code, failure.msg)
			return
		}
	}
	if failure := applyStreamSettings(settings); failure != nil {
		rsp.ErrRsp(c, failure.code, failure.msg)
		return
	}
	if err := applyVideoMonitorSettings(req.monitor()); err != nil {
		rsp.ErrRsp(c, -4, err.Error())
		return
	}
	if err := req.storeTransport(); err != nil {
		rsp.ErrRsp(c, -2, err.Error())
		return
	}

	rsp.OkRspWithData(c, videoCapabilities())
}

// applyStreamSettings applies the validated settings except the monitor ones,
// which make one final HDMI state after them.
func applyStreamSettings(settings []proto.SetScreenReq) *settingError {
	for _, setting := range settings {
		switch setting.Type {
		case "monitor", "portrait", "portrait_resolution":
			continue
		}
		if _, failure := applyScreenSetting(setting); failure != nil {
			return failure
		}
	}
	return nil
}
