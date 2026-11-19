package vm

import (
	"context"
	"encoding/base64"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"NanoKVM-Server/common"
	"NanoKVM-Server/proto"
)

// The overview page shows the host screen as a small still picture. It is
// taken at most once per previewMaxAge however many browsers ask, through a
// capture lease as a screenshot is: it starts no stream and counts as no
// viewer, and it does not switch on a capture that was switched off.
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
)

// Preview states other than a picture.
const (
	previewOK          = "ok"
	previewDisabled    = "disabled"    // the capture is switched off
	previewNoSignal    = "no-signal"   // nothing on the HDMI input
	previewBlocked     = "blocked"     // a 3840x2160 input has no JPEG
	previewUnavailable = "unavailable" // the capture did not give a frame
)

type screenPreview struct {
	State  string `json:"state"`
	JPEG   string `json:"jpeg,omitempty"` // base64
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

var preview struct {
	sync.Mutex
	last screenPreview
	at   time.Time
}

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
)

// ScreenPreview is GET /api/vm/screen/preview.
func (s *Service) ScreenPreview(c *gin.Context) {
	var rsp proto.Response
	c.Header("Cache-Control", "no-store")
	if isHdmiDisabled() {
		rsp.OkRspWithData(c, screenPreview{State: previewDisabled})
		return
	}
	preview.Lock()
	defer preview.Unlock()
	maxAge := previewMaxAge
	if preview.last.State != previewOK {
		maxAge = previewRetryAge
	}
	if preview.last.State == "" || time.Since(preview.at) >= maxAge {
		ctx, cancel := context.WithTimeout(c.Request.Context(), previewTimeout)
		preview.last = takeScreenPreview(ctx)
		cancel()
		preview.at = time.Now()
	}
	rsp.OkRspWithData(c, preview.last)
}

func takeScreenPreview(ctx context.Context) screenPreview {
	width, height := previewSize(previewScreen())
	release, claimFresh, err := previewLease(ctx)
	if err != nil {
		return screenPreview{State: previewUnavailable}
	}
	defer release()
	state := previewUnavailable
	for attempt := 0; attempt < previewAttempts && ctx.Err() == nil; attempt++ {
		data, result := previewRead(width, height, previewQuality)
		switch {
		case result == common.MjpegBlockedResult:
			return screenPreview{State: previewBlocked}
		case result >= 0 && result != 5 && len(data) > 0:
			// the first frame after a wake is the one from before it
			if claimFresh != nil && claimFresh() {
				claimFresh = nil
				continue
			}
			return screenPreview{
				State:  previewOK,
				JPEG:   base64.StdEncoding.EncodeToString(data),
				Width:  int(width),
				Height: int(height),
			}
		case result == 5:
			state = previewNoSignal
		}
		select {
		case <-ctx.Done():
		case <-time.After(previewRetry):
		}
	}
	return screenPreview{State: state}
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
