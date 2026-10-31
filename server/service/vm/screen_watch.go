package vm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/common"
	"NanoKVM-Server/internal/videogate"
	"NanoKVM-Server/service/notify"
)

// The screen watcher looks at the host's screen for a crash: a Windows stop
// screen or a kernel panic. A thumbnail every interval; once the screen has
// changed and then held still, the TPU classifier (nanokvm-screen, with its
// model add-on) says what it shows. A stop screen or a text console is then read by the
// OCR, when nobody watches the video (its models take the memory a viewer's
// video needs), and only text that names a crash makes a notification,
// with the lines read and a screenshot.
const (
	screenHelper = screenTextRoot + "/bin/nanokvm-screen"
	screenModel  = screenTextRoot + "/models/screen.cvimodel"
	// The classifier's input (tpu/models/screen/synth.py INPUT).
	screenThumbWidth, screenThumbHeight = 224, 128
	// The screenshot sent with a crash.
	screenShotWidth = 960
)

var (
	// screenWatchInterval: a stop screen shows for tens of seconds before
	// Windows restarts, a panic stays.
	screenWatchInterval = 10 * time.Second
	// A thumbnail differs from the last by more than this mean per byte:
	// the screen changed. A ticking "% complete" stays under it.
	screenChangeLevel = 2.0
	// The classifier's score for a stop screen or a console to be read.
	screenAlarmScore = 0.7
	// The least time between two OCR reads: a server console that scrolls
	// and settles again and again must not keep the TPU busy.
	screenReadGap = 2 * time.Minute
)

// Text that names a crash. Stop codes are upper case words joined by
// underscores in every language of Windows; the rest is English in any
// Windows and in Linux.
var (
	screenBSODText  = regexp.MustCompile(`(?i)stop ?code|ran into a problem|shut down to prevent damage|\*\*\* ?STOP|windows\.com/stopcode|\b[A-Z]{2,}(_[A-Z0-9]{2,}){2,}\b`)
	screenPanicText = regexp.MustCompile(`(?i)kernel panic|not syncing|\boops\b|\bBUG: |general protection fault|call trace|soft lockup|hard lockup|unable to handle (kernel|page)`)
)

// screenVerdict is the helper's reply.
type screenVerdict struct {
	Class  string             `json:"class"`
	Score  float64            `json:"score"`
	Scores map[string]float64 `json:"scores"`
	MS     int                `json:"ms"`
}

// ScreenWatchAvailable: the classifier's model is installed and the OCR is
// installed and on.
func ScreenWatchAvailable() bool {
	if !ScreenTextAvailable() {
		return false
	}
	_, helper := os.Stat(screenHelper)
	_, model := os.Stat(screenModel)
	return helper == nil && model == nil
}

type screenWatch struct {
	wants     func(notify.Kind) bool
	emit      func(notify.Event)
	available func() bool
	// thumb takes the classifier's picture; ok is false without a signal.
	thumb    func(ctx context.Context) ([]byte, bool)
	classify func(ctx context.Context, planes []byte) (screenVerdict, error)
	viewers  func() int
	// begin admits a background read; its context is cancelled when video
	// starts (videogate).
	begin func(ctx context.Context) (context.Context, func(), bool)
	// read is the OCR of the whole screen, with the frame it read.
	read func(ctx context.Context) (screenRead, error)
	now  func() time.Time

	last     []byte
	pending  bool // changed since the last verdict
	reported bool // the crash on the screen was reported
	judged   bool // a verdict was logged since the start
	readAt   time.Time
}

func newScreenWatch() *screenWatch {
	return &screenWatch{
		wants:     notify.Wants,
		emit:      notify.Emit,
		available: ScreenWatchAvailable,
		thumb:     screenThumb,
		classify:  classifyScreen,
		viewers:   func() int { return HdmiState().ViewerCount },
		begin:     videogate.Begin,
		read:      readScreenLines,
		now:       time.Now,
	}
}

func (w *screenWatch) reset() {
	w.last, w.pending, w.reported = nil, false, false
}

func (w *screenWatch) step(ctx context.Context) {
	if !w.wants(notify.KindScreen) || !w.available() {
		w.reset()
		return
	}
	planes, ok := w.thumb(ctx)
	if !ok {
		w.reset()
		return
	}
	if w.last == nil || meanDifference(w.last, planes) > screenChangeLevel {
		// Changed (or the first look): judge it once it holds still.
		w.last, w.pending = planes, true
		return
	}
	if !w.pending {
		return
	}
	verdict, err := w.classify(ctx, planes)
	if err != nil {
		// Tried again at the next look (the TPU may be reading the screen).
		log.Warnf("screen watch: %v", err)
		return
	}
	w.pending = false
	alarm := (verdict.Class == "bsod" || verdict.Class == "console") && verdict.Score >= screenAlarmScore
	if !w.judged || alarm {
		log.Infof("screen watch: %s %.2f (%d ms)", verdict.Class, verdict.Score, verdict.MS)
		w.judged = true
	} else {
		log.Debugf("screen watch: %s %.2f (%d ms)", verdict.Class, verdict.Score, verdict.MS)
	}
	if !alarm {
		w.reported = false
		return
	}
	if w.reported {
		return
	}
	w.check(ctx, verdict, planes)
}

// check reads a suspect screen, the thumbnail judged, and reports a crash it
// names. Somebody who watches the video sees it: then nothing is read or
// reported, and a viewer who comes during the read cancels it. A read that
// failed is tried again while the screen stays (at once when the TPU was
// busy, after the gap otherwise).
func (w *screenWatch) check(ctx context.Context, verdict screenVerdict, judged []byte) {
	if w.viewers() > 0 {
		return
	}
	now := w.now()
	if !w.readAt.IsZero() && now.Sub(w.readAt) < screenReadGap {
		// Read too recently: judge it again when the gap is over.
		w.pending = true
		return
	}
	ctx, end, ok := w.begin(ctx)
	if !ok {
		return // video runs
	}
	previous := w.readAt
	w.readAt = now
	got, err := w.read(ctx)
	cancelled := ctx.Err() != nil
	end()
	if cancelled {
		// A viewer came; the read did not count.
		w.readAt = previous
		log.Infof("screen watch: OCR gave way to a viewer")
		return
	}
	if err != nil {
		var readErr *ScreenTextError
		if errors.As(err, &readErr) && readErr.Code == -2 {
			w.readAt = previous // somebody else was reading: no read happened
		}
		log.Warnf("screen watch: OCR: %v", err)
		w.pending = true
		return
	}
	// The text and the picture sent are those of the frame the OCR read; a
	// frame that is no longer the screen judged is judged anew.
	if got.planes != nil {
		thumb := shrinkPlanes(got.planes, got.width, got.height, screenThumbWidth, screenThumbHeight)
		if meanDifference(thumb, judged) > screenChangeLevel {
			log.Infof("screen watch: the screen changed before it was read")
			w.last, w.pending = thumb, true
			return
		}
	}
	bsod := verdict.Class == "bsod"
	switch {
	case screenBSODText.MatchString(got.text) && bsod:
		w.report("screen.bsod", crashLines(got.text, screenBSODText), got)
	case screenPanicText.MatchString(got.text):
		w.report("screen.panic", crashLines(got.text, screenPanicText), got)
	}
}

func (w *screenWatch) report(message, details string, got screenRead) {
	w.reported = true
	w.emit(notify.Event{Kind: notify.KindScreen, Severity: notify.SeverityCritical, Message: message,
		Details: details, Image: screenJPEG(got.planes, got.width, got.height)})
}

// crashLines are the lines of the screen from the first that names the
// crash, at most 20.
func crashLines(text string, crash *regexp.Regexp) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i, line := range lines {
		if crash.MatchString(line) {
			lines = lines[max(0, i-2):]
			break
		}
	}
	return strings.Join(lines[:min(20, len(lines))], "\n")
}

func meanDifference(a, b []byte) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 255
	}
	var sum int64
	for i := range a {
		d := int64(a[i]) - int64(b[i])
		if d < 0 {
			d = -d
		}
		sum += d
	}
	return float64(sum) / float64(len(a))
}

// screenPlanes reads the screen at the input's size (the native read takes
// no other) while capture runs; ok is false without a signal or capture,
// and for an input over Full HD, which VPSS cannot hand out as planes.
func screenPlanes(ctx context.Context) (planes []byte, width, height int, ok bool) {
	if signal, ok := notifyHDMISignal(); !ok || !signal {
		return nil, 0, 0, false
	}
	if _, _, err := screenTextInput(); err != nil {
		return nil, 0, 0, false
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	release, _, err := AcquireHdmiCaptureLeaseForRead(ctx)
	if err != nil {
		return nil, 0, 0, false
	}
	defer release()
	for attempt := 0; attempt < 3; attempt++ {
		width, height, err = screenTextInput()
		if err != nil {
			break
		}
		if planes, err = readPlanar(width, height); err == nil {
			screenPlanesFailure = ""
			return planes, width, height, true
		}
		// Only a frame late or a size being detected is worth another try.
		if !errors.Is(err, common.ErrPlanarPictureTimeout) && !errors.Is(err, common.ErrPlanarCaptureNotReady) &&
			!errors.Is(err, common.ErrPlanarInputChanged) {
			break
		}
		select {
		case <-ctx.Done():
			attempt = 3
		case <-time.After(300 * time.Millisecond):
		}
	}
	// A failure is logged once, not every look.
	if err != nil && err.Error() != screenPlanesFailure {
		screenPlanesFailure = err.Error()
		log.Warnf("screen watch: no picture of the screen: %v", err)
	}
	return nil, 0, 0, false
}

// screenPlanesFailure is the last failure logged (the watcher's goroutine
// alone reads it).
var screenPlanesFailure string

func screenThumb(ctx context.Context) ([]byte, bool) {
	planes, width, height, ok := screenPlanes(ctx)
	if !ok {
		return nil, false
	}
	return shrinkPlanes(planes, width, height, screenThumbWidth, screenThumbHeight), true
}

// shrinkPlanes scales B, G and R planes down to toW x toH, each pixel the
// mean of the block it covers (the classifier learnt from pictures scaled
// that way). The aspect is not kept: VPSS scales the whole frame too. A
// block of 4 or more on a side is sampled at every other row and column:
// a quarter of the reads, every ten seconds on the device.
func shrinkPlanes(planes []byte, width, height, toW, toH int) []byte {
	out := make([]byte, toW*toH*3)
	n := width * height
	step := 1
	if width >= 4*toW && height >= 4*toH {
		step = 2
	}
	for c := 0; c < 3; c++ {
		src := planes[c*n : (c+1)*n]
		dst := out[c*toW*toH : (c+1)*toW*toH]
		for oy := 0; oy < toH; oy++ {
			y0, y1 := oy*height/toH, max(oy*height/toH+1, (oy+1)*height/toH)
			for ox := 0; ox < toW; ox++ {
				x0, x1 := ox*width/toW, max(ox*width/toW+1, (ox+1)*width/toW)
				sum, count := 0, 0
				for y := y0; y < y1; y += step {
					row := src[y*width : y*width+x1]
					for x := x0; x < x1; x += step {
						sum += int(row[x])
						count++
					}
				}
				dst[oy*toW+ox] = byte(sum / count)
			}
		}
	}
	return out
}

func classifyScreen(ctx context.Context, planes []byte) (screenVerdict, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	// The TPU takes one model at a time here: never beside an OCR read.
	if !screenTextMutex.TryLock() {
		return screenVerdict{}, errors.New("the screen is being read")
	}
	defer screenTextMutex.Unlock()
	if err := loadTPUDriver(ctx); err != nil {
		return screenVerdict{}, err
	}
	input := fmt.Appendf(nil, "P %d %d\n", screenThumbWidth, screenThumbHeight)
	cmd := exec.CommandContext(ctx, screenHelper, screenModel)
	cmd.Stdin = bytes.NewReader(append(input, planes...))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return screenVerdict{}, fmt.Errorf("%v: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	var verdict screenVerdict
	if err := json.Unmarshal(out, &verdict); err != nil {
		return screenVerdict{}, fmt.Errorf("classifier output: %v", err)
	}
	return verdict, nil
}

// screenRead is an OCR read: the lines, and the frame they were read from as
// B, G and R planes of width x height (nil when it was not kept).
type screenRead struct {
	text          string
	planes        []byte
	width, height int
}

func readScreenLines(ctx context.Context) (screenRead, error) {
	reply, err := ReadScreenText(ctx, ScreenTextRequest{keepPlanes: true})
	if err != nil {
		return screenRead{}, err
	}
	return screenRead{text: ScreenTextLines(reply.Boxes), planes: reply.planes, width: reply.Width, height: reply.Height}, nil
}

// screenJPEG is a JPEG of a frame at most screenShotWidth wide, or nil.
func screenJPEG(planes []byte, width, height int) []byte {
	if len(planes) != width*height*3 || width == 0 {
		return nil
	}
	if width > screenShotWidth {
		toH := height * screenShotWidth / width
		planes, width, height = shrinkPlanes(planes, width, height, screenShotWidth, toH), screenShotWidth, toH
	}
	return planesJPEG(planes, width, height)
}

// planesJPEG encodes B, G and R planes as a JPEG.
func planesJPEG(planes []byte, width, height int) []byte {
	n := width * height
	if len(planes) != n*3 {
		return nil
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < n; i++ {
		img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3] = planes[2*n+i], planes[n+i], planes[i], 255
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		return nil
	}
	return buf.Bytes()
}

var startScreenWatchOnce sync.Once

// StartScreenWatch starts watching the screen for crashes.
func StartScreenWatch() {
	startScreenWatchOnce.Do(func() {
		w := newScreenWatch()
		go func() {
			ticker := time.NewTicker(screenWatchInterval)
			defer ticker.Stop()
			for range ticker.C {
				w.step(context.Background())
			}
		}()
	})
}
