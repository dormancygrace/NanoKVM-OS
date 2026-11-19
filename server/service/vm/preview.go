package vm

import (
	"context"
	"encoding/base64"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"NanoKVM-Server/common"
	"NanoKVM-Server/internal/videogate"
	"NanoKVM-Server/proto"
)

// The overview page shows the host screen as a small still picture. It is
// taken at most once per previewMaxAge however many browsers ask, through a
// capture lease as a screenshot is: it starts no stream and counts as no
// viewer, and it does not switch on a capture that was switched off.
//
// A still of its own size can make the capture set an output up again, and
// with it the encoder of a video session. Which outputs a session and a
// still share depends on the codec, the chroma the capture really uses and
// the sizes, so no picture is taken while anyone watches: the last one is
// shown, marked as held. The preview is background work behind the video
// gate. It does not start while video holds the gate, and a viewer that
// arrives while it reads ends it and waits for that before it wakes the
// capture or sets up an encoder.
const (
	previewMaxWidth  = 640
	previewMaxHeight = 360
	previewQuality   = 60
	previewMaxAge    = 5 * time.Second
	// a state without a picture is asked again sooner: a capture that was
	// just switched on has one in a moment
	previewRetryAge = time.Second
	previewTimeout  = 4 * time.Second
	previewAttempts = 10
	previewRetry    = 100 * time.Millisecond
	// ReadMjpeg: with frame detection on, the picture did not change
	previewUnchanged = 5
)

// Preview states.
const (
	previewOK          = "ok"
	previewDisabled    = "disabled"    // the capture is switched off
	previewNoSignal    = "no-signal"   // nothing on the HDMI input
	previewBlocked     = "blocked"     // a 3840x2160 input has no JPEG
	previewInUse       = "in-use"      // a video session is open and there is no earlier picture
	previewUnavailable = "unavailable" // the capture did not give a frame
)

type screenPreview struct {
	State  string `json:"state"`
	JPEG   string `json:"jpeg,omitempty"` // base64
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
	// Held: the picture is an earlier one, not renewed while a video session
	// is open.
	Held bool `json:"held,omitempty"`
}

var preview struct {
	sync.Mutex
	last screenPreview
	at   time.Time
	// the last picture of this capture: what an unchanged screen still
	// shows, and what is held while a video session is open
	picture screenPreview
	// the switching of the capture last and picture belong to
	capture uint64
}

// previewCapture counts the times the capture was switched on or off. A
// picture from before the last one is of a screen that may be gone.
var previewCapture atomic.Uint64

// previewCaptureSwitched is called when the capture is switched on or off.
func previewCaptureSwitched() { previewCapture.Add(1) }

var (
	previewLease = AcquireHdmiCaptureLeaseForRead
	previewRead  = func(width, height, quality uint16) ([]byte, int) {
		return common.GetKvmVision().ReadMjpeg(width, height, quality)
	}
	previewScreen = func() (uint16, uint16) {
		common.CheckScreen()
		screen := common.GetScreen()
		return screen.Width, screen.Height
	}
	previewViewers = func() int { return hdmiState().ViewerCount }
	previewBegin   = videogate.Begin
	previewSignal  = func() bool { return getHdmiSignal() }
	// previewPauseDetect switches frame detection off for a read that has
	// to give a picture, and returns what puts the saved choice back.
	previewPauseDetect = func() func() {
		if !common.FrameDetectEnabled() {
			return func() {}
		}
		vision := common.GetKvmVision()
		vision.SetFrameDetect(0)
		return func() {
			vision.SetFrameDetect(common.FrameDetectFrames(common.FrameDetectEnabled()))
		}
	}
)

// ScreenPreview is GET /api/vm/screen/preview.
func (s *Service) ScreenPreview(c *gin.Context) {
	var rsp proto.Response
	c.Header("Cache-Control", "no-store")
	rsp.OkRspWithData(c, screenPreviewNow(c.Request.Context()))
}

func screenPreviewNow(ctx context.Context) screenPreview {
	preview.Lock()
	defer preview.Unlock()
	capture := previewCapture.Load()
	if preview.capture != capture {
		preview.capture = capture
		preview.picture = screenPreview{}
		preview.last = screenPreview{}
	}
	if isHdmiDisabled() {
		return screenPreview{State: previewDisabled}
	}
	if previewViewers() > 0 {
		return heldScreenPreview()
	}
	maxAge := previewMaxAge
	if preview.last.State != previewOK {
		maxAge = previewRetryAge
	}
	if preview.last.State != "" && time.Since(preview.at) < maxAge {
		return preview.last
	}
	work, end, ok := previewBegin(ctx)
	if !ok {
		// A video session is starting, or other background work has the
		// capture: the picture there is, for now.
		if previewViewers() > 0 {
			return heldScreenPreview()
		}
		if preview.picture.State == previewOK {
			return preview.picture
		}
		return screenPreview{State: previewUnavailable}
	}
	read, cancel := context.WithTimeout(work, previewTimeout)
	taken := takeScreenPreview(read)
	cancel()
	// ended by video that took the gate: what it read is not kept
	yielded := work.Err() != nil
	end()
	if yielded || previewCapture.Load() != capture {
		return heldScreenPreview()
	}
	preview.last = taken
	preview.at = time.Now()
	return preview.last
}

// heldScreenPreview is the answer while a video session is open. preview is
// locked.
func heldScreenPreview() screenPreview {
	if preview.picture.State != previewOK {
		return screenPreview{State: previewInUse}
	}
	held := preview.picture
	held.Held = true
	return held
}

// takeScreenPreview reads one picture. preview is locked.
func takeScreenPreview(ctx context.Context) screenPreview {
	width, height := previewSize(previewScreen())
	release, claimFresh, err := previewLease(ctx)
	if err != nil {
		return screenPreview{State: previewUnavailable}
	}
	defer release()
	restoreDetect := func() {}
	defer func() { restoreDetect() }()
	paused := false
	for attempt := 0; attempt < previewAttempts; attempt++ {
		// Before every read: video that takes the gate cancels ctx and waits
		// for this to return.
		if ctx.Err() != nil {
			return screenPreview{State: previewUnavailable}
		}
		data, result := previewRead(width, height, previewQuality)
		switch {
		case result == common.MjpegBlockedResult:
			preview.picture = screenPreview{}
			return screenPreview{State: previewBlocked}
		case result == previewUnchanged:
			// the screen is what the last picture shows
			if preview.picture.State == previewOK {
				return preview.picture
			}
			// no picture to show yet: read one without the detection
			if !paused {
				paused = true
				restoreDetect = previewPauseDetect()
				continue
			}
		case result >= 0 && len(data) > 0:
			// the first frame after a wake is the one from before it
			if claimFresh != nil && claimFresh() {
				claimFresh = nil
				continue
			}
			preview.picture = screenPreview{
				State:  previewOK,
				JPEG:   base64.StdEncoding.EncodeToString(data),
				Width:  int(width),
				Height: int(height),
			}
			return preview.picture
		}
		select {
		case <-ctx.Done():
		case <-time.After(previewRetry):
		}
	}
	// no frame: without a signal that is the reason, and the last picture is
	// of a screen that is gone
	if !previewSignal() {
		preview.picture = screenPreview{}
		return screenPreview{State: previewNoSignal}
	}
	return screenPreview{State: previewUnavailable}
}

// previewSize fits the stream size into the preview bounds, keeping its
// shape, with even sides as the encoder wants them.
func previewSize(width, height uint16) (uint16, uint16) {
	w, h := int(width), int(height)
	if w <= 0 || h <= 0 {
		return previewMaxWidth, previewMaxHeight
	}
	if w > previewMaxWidth {
		h = h * previewMaxWidth / w
		w = previewMaxWidth
	}
	if h > previewMaxHeight {
		w = w * previewMaxHeight / h
		h = previewMaxHeight
	}
	w, h = w&^1, h&^1
	if w < 2 {
		w = 2
	}
	if h < 2 {
		h = 2
	}
	return uint16(w), uint16(h)
}
