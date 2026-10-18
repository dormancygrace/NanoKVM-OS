package vm

import (
	"context"
	"encoding/binary"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

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
)

var captureInput = common.CaptureInputSize

type monitorModeCapability struct {
	Height uint16 `json:"height"`
	Width  uint16 `json:"width"`
	// Auto (height 0): the size it stands for; 0 for the stock profile.
	EffectiveWidth  uint16 `json:"effectiveWidth,omitempty"`
	EffectiveHeight uint16 `json:"effectiveHeight,omitempty"`
	Rates           []int  `json:"rates"`
	Available       bool   `json:"available"`
	Reason          string `json:"reason,omitempty"`
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
			// Only when the largest video memory mode would capture it.
			largest := common.VideoPool{MiB: videoMemoryModes[len(videoMemoryModes)-1].mib}
			supported := common.PortraitSupportedFor(largest)
			if resolution == 2560 {
				supported = common.PortraitMaxSupportedFor(largest)
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
		// The H.264 encoder takes at most 4096x2304: 1296x2304 is the tallest
		// portrait for browsers without H.265, 1440x2560 needs H.265.
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
		"videoMemory":    videoMemoryCapabilities(readVideoMemoryStatus(videoMemoryFiles)),
	}
}

type videoMemoryModeCapability struct {
	Mode      string `json:"mode"`
	MiB       int    `json:"mib"`
	Installed bool   `json:"installed"`
}

// videoMemoryCapabilities tells clients which mode applying a resolution
// switches to: a resolution that needs another installed mode is applied with
// fitVideoMemory and restarts the device.
func videoMemoryCapabilities(status videoMemoryStatus) gin.H {
	modes := make([]videoMemoryModeCapability, 0, len(videoMemoryModes))
	for _, m := range videoMemoryModes {
		modes = append(modes, videoMemoryModeCapability{m.name, m.mib, slices.Contains(status.Modes, m.name)})
	}
	return gin.H{"active": status.Active, "selected": status.Selected, "modes": modes}
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
	ConfirmPowerCycle bool `json:"confirmPowerCycle"`
	// FitVideoMemory lets a monitor change switch the video memory mode: to
	// the smallest installed mode that captures the new monitor, larger or
	// smaller, which restarts the device.
	FitVideoMemory     bool  `json:"fitVideoMemory"`
	Type               *int  `json:"type"`
	Quality            *int  `json:"quality"`
	BitRate            *int  `json:"bitRate"`
	MjpegChroma        *int  `json:"mjpegChroma"`
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

func (r VideoSettingsReq) changesMonitor() bool {
	return r.Monitor != nil || r.Portrait != nil || r.PortraitResolution != nil
}

var videoSettingsMutex sync.Mutex

// Seams for tests.
var (
	applyVideoMonitorSettings = common.ApplyMonitorSettings
	saveMonitorForRestart     = common.SaveMonitorSettingsForRestart
	holdMonitorForRestart     = common.HoldMonitorProfileForRestart
	monitorStateAfter         = common.MonitorStateAfter
	bootedVideoPool           = common.BootedVideoPool
	restartDevice             = func(c *gin.Context) {
		// Deliver the response first, as for a TLS change; the caller keeps
		// its locks until the restart is under way.
		c.Writer.Flush()
		time.Sleep(time.Second)
		log.Println("reboot system: video memory mode switch")
		if err := exec.Command("reboot").Run(); err != nil {
			log.Errorf("failed to reboot: %s", err)
		}
	}
)

// Set once a video memory switch has been activated: the device is about to
// restart and accepts no further video or memory changes.
var videoMemoryRestartPending atomic.Bool

// videoMemorySwitch is a change of the video memory mode that a request needs.
type videoMemorySwitch struct {
	previous string // the mode selected before, to undo the activation
	target   string
	pool     common.VideoPool
}

// targetVideoMemoryMode is the smallest installed mode that captures a monitor.
func targetVideoMemoryMode(installed []string, state common.MonitorState) (string, bool) {
	for _, mode := range videoMemoryModes {
		pool, _ := videoMemoryPool(mode.name)
		if slices.Contains(installed, mode.name) && state.FitsVideoPool(pool) == nil {
			return mode.name, true
		}
	}
	return "", false
}

// planVideoMemorySwitch tells whether a request switches the video memory
// mode: only with fitVideoMemory, only when it changes the monitor, and only
// when the smallest installed mode that captures the new monitor is not the
// booted one. Otherwise the request is applied as it always was.
func planVideoMemorySwitch(req *VideoSettingsReq) (videoMemorySwitch, bool) {
	if !req.FitVideoMemory || !req.changesMonitor() {
		return videoMemorySwitch{}, false
	}
	status := readVideoMemoryStatus(videoMemoryFiles)
	if status.Active == "unknown" {
		return videoMemorySwitch{}, false
	}
	target, ok := targetVideoMemoryMode(status.Modes, monitorStateAfter(req.monitor()))
	if !ok || target == status.Active {
		return videoMemorySwitch{}, false
	}
	pool, _ := videoMemoryPool(target)
	return videoMemorySwitch{previous: status.Selected, target: target, pool: pool}, true
}

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
	if videoMemoryRestartPending.Load() {
		rsp.ErrRsp(c, -6, common.ErrMonitorRestartPending.Error())
		return
	}

	booted := bootedVideoPool()
	change, switching := planVideoMemorySwitch(&req)
	pool := booted
	if switching {
		pool = change.pool
	}
	settings := req.settings()
	for _, setting := range settings {
		failure := validateScreenSettingFor(setting, &req, pool)
		// A stream limit applies at once and after the restart.
		if failure == nil && switching && setting.Type == "resolution" {
			failure = validateScreenSettingFor(setting, &req, booted)
		}
		if failure != nil {
			rsp.ErrRsp(c, failure.code, failure.msg)
			return
		}
	}
	if switching {
		s.switchVideoMemory(c, &req, settings, change, booted)
		return
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

// switchVideoMemory applies a validated request together with the video
// memory mode it needs, then restarts the device. A monitor that the booted
// pool cannot capture is only saved, and the first start after the restart
// programs it; one that fits (a switch to a smaller mode) is programmed now,
// as a Cube must be. The mode is activated before anything else changes, so a
// failed activation leaves everything as it was; a later failure selects the
// previous mode again.
func (s *Service) switchVideoMemory(c *gin.Context, req *VideoSettingsReq, settings []proto.SetScreenReq,
	change videoMemorySwitch, booted common.VideoPool) {
	var rsp proto.Response
	lock, err := lockVideoMemory()
	if err != nil {
		rsp.ErrRsp(c, -2, err.Error())
		return
	}
	defer lock.Close()
	memoryMutation.Lock()
	defer memoryMutation.Unlock()

	monitor := req.monitor()
	var restore func() error
	if monitorStateAfter(monitor).FitsVideoPool(booted) != nil {
		if restore, err = saveMonitorForRestart(monitor, change.pool); err != nil {
			rsp.ErrRsp(c, -4, err.Error())
			return
		}
	}
	if err = activateVideoMemoryMode(c.Request.Context(), change.target); err != nil {
		if restore != nil {
			err = errors.Join(err, restore())
		}
		rsp.ErrRsp(c, -2, err.Error())
		return
	}
	undo := func(code int, cause error) {
		errs := []error{cause}
		if err := activateVideoMemoryMode(context.Background(), change.previous); err != nil {
			errs = append(errs, err)
		}
		if restore != nil {
			errs = append(errs, restore())
		}
		rsp.ErrRsp(c, code, errors.Join(errs...).Error())
	}
	if failure := applyStreamSettings(settings); failure != nil {
		undo(failure.code, errors.New(failure.msg))
		return
	}
	if restore == nil {
		if err = applyVideoMonitorSettings(monitor); err != nil {
			undo(-4, err)
			return
		}
		holdMonitorForRestart()
	}
	if err = req.storeTransport(); err != nil {
		undo(-2, err)
		return
	}

	videoMemoryRestartPending.Store(true)
	log.Infof("video memory mode %s selected for the monitor; restarting", change.target)
	rsp.OkRspWithData(c, gin.H{"restart": true, "videoMemoryMode": change.target})
	restartDevice(c)
}
