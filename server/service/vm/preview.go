package vm

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"NanoKVM-Server/common"
	"NanoKVM-Server/proto"
)

// The overview page shows the host screen as a small still picture. It is
// taken at most once per previewMaxAge however many browsers ask, through a
// capture lease as a screenshot is: it starts no stream and counts as no
// viewer, and it does not switch on a capture that was switched off.
//
// The picture comes from the VPSS channel that serves pictures of the
// screen (ReadBGRScaled), scaled there and made a JPEG here. The outputs a
// video session reads and its encoder are not touched, so it is taken next
// to a session as well. It carries the watermark the streams carry; when
// that cannot be put on there is no picture.
const (
	previewMaxWidth  = 640
	previewMaxHeight = 360
	previewQuality   = 60
	previewMaxAge    = 5 * time.Second
	// a state without a picture is asked again sooner: a capture that was
	// just switched on has one in a moment
	previewRetryAge = time.Second
	previewTimeout  = 4 * time.Second
	previewAttempts = 6
	previewRetry    = 200 * time.Millisecond
)

// Preview states.
const (
	previewOK          = "ok"
	previewDisabled    = "disabled"    // the capture is switched off
	previewNoSignal    = "no-signal"   // nothing on the HDMI input
	previewUnavailable = "unavailable" // the capture did not give a picture
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
	// the switching of the capture that last belongs to
	capture uint64
}

// previewCapture counts the times the capture was switched on or off. A
// picture from before the last one is of a screen that may be gone.
var previewCapture atomic.Uint64

// previewCaptureSwitched is called when the capture is switched on or off,
// and when the watermark changes: a picture with the old one is not shown.
func previewCaptureSwitched() { previewCapture.Add(1) }

var (
	previewLease = AcquireHdmiCaptureLeaseForRead
	previewRead  = func(width, height int) ([]byte, error) {
		return common.GetKvmVision().ReadBGRScaled(width, height)
	}
	previewInput    = common.CaptureInputSize
	previewSignal   = func() bool { return getHdmiSignal() }
	previewUnmarked = common.WatermarkUnapplied
	previewViewers  = func() int { return hdmiState().ViewerCount }
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
		preview.last = screenPreview{}
	}
	if isHdmiDisabled() {
		return screenPreview{State: previewDisabled}
	}
	// A watermark is saved and the library does not have it: a picture now
	// would be without it.
	if previewUnmarked() {
		preview.last = screenPreview{}
		return screenPreview{State: previewUnavailable}
	}
	maxAge := previewMaxAge
	if preview.last.State != previewOK {
		maxAge = previewRetryAge
	}
	if preview.last.State != "" && time.Since(preview.at) < maxAge {
		return preview.last
	}
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	taken := takeScreenPreview(ctx)
	cancel()
	// switched while it was read: the picture may be of the screen before
	if previewCapture.Load() != capture {
		return screenPreview{State: previewUnavailable}
	}
	preview.last = taken
	preview.at = time.Now()
	return preview.last
}

// takeScreenPreview reads one picture. preview is locked.
func takeScreenPreview(ctx context.Context) screenPreview {
	release, claimFresh, err := previewLease(ctx)
	if err != nil {
		return screenPreview{State: previewUnavailable}
	}
	defer release()
	// A read that gets no picture holds the capture for its whole wait, and
	// a video session waits with it: next to one there is a single try, and
	// the next request is the next.
	attempts := previewAttempts
	if previewViewers() > 0 {
		attempts = 1
	}
	for attempt := 0; attempt < attempts && ctx.Err() == nil; attempt++ {
		// a viewer that came while the last try waited
		if attempt > 0 && previewViewers() > 0 {
			break
		}
		// read again every time: a new input size makes the last one stale
		width, height := previewSize(previewInput())
		planes, err := previewRead(width, height)
		switch {
		case err == nil:
			// the first picture after a wake is the one from before it
			if claimFresh != nil && claimFresh() {
				claimFresh = nil
				continue
			}
			data, err := previewJPEG(planes, width, height)
			if err != nil {
				return screenPreview{State: previewUnavailable}
			}
			return screenPreview{
				State:  previewOK,
				JPEG:   base64.StdEncoding.EncodeToString(data),
				Width:  width,
				Height: height,
			}
		case errors.Is(err, common.ErrPlanarPictureNoSignal):
			return screenPreview{State: previewNoSignal}
		case errors.Is(err, common.ErrPlanarPictureTimeout),
			errors.Is(err, common.ErrPlanarCaptureNotReady),
			errors.Is(err, common.ErrPlanarInputChanged):
			// worth another try
		default:
			return noScreenPreview()
		}
		select {
		case <-ctx.Done():
		case <-time.After(previewRetry):
		}
	}
	return noScreenPreview()
}

// noScreenPreview is the answer without a picture: with nothing on the
// input that is the reason.
func noScreenPreview() screenPreview {
	if !previewSignal() {
		return screenPreview{State: previewNoSignal}
	}
	return screenPreview{State: previewUnavailable}
}

// previewJPEG makes a JPEG of B, G and R planes. The colour is halved both
// ways (4:2:0), each sample the mean of its four pixels.
func previewJPEG(planes []byte, width, height int) ([]byte, error) {
	n := width * height
	if width < 2 || height < 2 || width%2 != 0 || height%2 != 0 || len(planes) < 3*n {
		return nil, errors.New("not a picture")
	}
	b, g, r := planes[:n], planes[n:2*n], planes[2*n:3*n]
	img := image.NewYCbCr(image.Rect(0, 0, width, height), image.YCbCrSubsampleRatio420)
	for y := 0; y < height; y += 2 {
		for x := 0; x < width; x += 2 {
			var sr, sg, sb int
			for _, at := range [4]int{y*width + x, y*width + x + 1, (y+1)*width + x, (y+1)*width + x + 1} {
				luma, _, _ := color.RGBToYCbCr(r[at], g[at], b[at])
				img.Y[at/width*img.YStride+at%width] = luma
				sr, sg, sb = sr+int(r[at]), sg+int(g[at]), sb+int(b[at])
			}
			_, cb, cr := color.RGBToYCbCr(uint8((sr+2)/4), uint8((sg+2)/4), uint8((sb+2)/4))
			at := y/2*img.CStride + x/2
			img.Cb[at], img.Cr[at] = cb, cr
		}
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: previewQuality}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// previewSize fits the input size into the preview bounds, keeping its
// shape, with even sides as VPSS and the JPEG want them. A picture is never
// larger than the input.
func previewSize(width, height int) (int, int) {
	w, h := width, height
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
	return max(w&^1, 16), max(h&^1, 16)
}
