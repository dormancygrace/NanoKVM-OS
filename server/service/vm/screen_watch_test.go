package vm

import (
	"bytes"
	"context"
	"errors"
	"image/jpeg"
	"strings"
	"testing"
	"time"

	"NanoKVM-Server/internal/videogate"
	"NanoKVM-Server/service/notify"
)

type watchedScreen struct {
	picture     byte // every byte of the thumbnail
	signal      bool
	verdict     screenVerdict
	viewers     int
	text        string
	ocrErr      error
	ocrErrs     []error // the next reads' errors, first first
	classifyErr error
	classify    int
	reads       int
	events      []notify.Event
	clock       time.Time
	// duringRead runs inside the OCR read, after its frame was taken.
	duringRead func(ctx context.Context)
}

// thumbOf is a flat thumbnail of one value, at the classifier's size.
func thumbOf(value byte) []byte {
	return bytes.Repeat([]byte{value}, screenThumbWidth*screenThumbHeight*3)
}

func (s *watchedScreen) watch() *screenWatch {
	return &screenWatch{
		wants:     func(k notify.Kind) bool { return k == notify.KindScreen },
		emit:      func(e notify.Event) { s.events = append(s.events, e) },
		available: func() bool { return true },
		thumb: func(context.Context) ([]byte, bool) {
			return thumbOf(s.picture), s.signal
		},
		classify: func(context.Context, []byte) (screenVerdict, error) {
			s.classify++
			if err := s.classifyErr; err != nil {
				s.classifyErr = nil
				return screenVerdict{}, err
			}
			return s.verdict, nil
		},
		viewers: func() int { return s.viewers },
		begin:   videogate.Begin,
		read: func(ctx context.Context) (screenRead, error) {
			s.reads++
			// The frame is the screen as the read starts.
			got := screenRead{text: s.text, planes: thumbOf(s.picture), width: screenThumbWidth, height: screenThumbHeight}
			if s.duringRead != nil {
				s.duringRead(ctx)
			}
			if len(s.ocrErrs) > 0 {
				err := s.ocrErrs[0]
				s.ocrErrs = s.ocrErrs[1:]
				return screenRead{}, err
			}
			if s.ocrErr != nil {
				return screenRead{}, s.ocrErr
			}
			return got, nil
		},
		now: func() time.Time { return s.clock },
	}
}

// imageValue is the mean of a JPEG's blue channel.
func imageValue(t *testing.T, data []byte) int {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	_, _, b, _ := img.At(img.Bounds().Dx()/2, img.Bounds().Dy()/2).RGBA()
	return int(b >> 8)
}

// run looks n times, screenWatchInterval apart.
func (s *watchedScreen) run(w *screenWatch, n int) {
	for i := 0; i < n; i++ {
		w.step(context.Background())
		s.clock = s.clock.Add(screenWatchInterval)
	}
}

const panicScreen = "[ 12.3] VFS: Cannot open root device\nKernel panic - not syncing: VFS: Unable to mount root fs\nCPU: 0 PID: 1\nCall Trace:\n dump_stack+0x10"

func TestScreenWatchJudgesASettledScreenOnce(t *testing.T) {
	s := &watchedScreen{picture: 10, signal: true, verdict: screenVerdict{Class: "normal", Score: 0.99}}
	w := s.watch()
	s.run(w, 5)
	if s.classify != 1 {
		t.Fatalf("a still screen was judged %d times", s.classify)
	}
	// A small change (a ticking counter) is not a new screen.
	s.picture = 11
	s.run(w, 3)
	if s.classify != 1 {
		t.Fatalf("a small change was judged: %d", s.classify)
	}
	// A new screen is judged once it has held still.
	s.picture = 200
	s.run(w, 1)
	if s.classify != 1 {
		t.Fatal("judged while still changing")
	}
	s.run(w, 3)
	if s.classify != 2 {
		t.Fatalf("judged %d times", s.classify)
	}
}

func TestScreenWatchRetriesAFailedVerdict(t *testing.T) {
	s := &watchedScreen{picture: 0, signal: true, verdict: screenVerdict{Class: "console", Score: 0.95},
		text: panicScreen, classifyErr: errors.New("the screen is being read")}
	w := s.watch()
	s.run(w, 4)
	if s.classify != 2 || len(s.events) != 1 {
		t.Fatalf("classified %d times, %d events", s.classify, len(s.events))
	}
}

func TestScreenWatchReportsAPanicReadByOCR(t *testing.T) {
	s := &watchedScreen{picture: 0, signal: true, verdict: screenVerdict{Class: "console", Score: 0.95}, text: panicScreen}
	w := s.watch()
	s.run(w, 4)
	if len(s.events) != 1 || s.events[0].Message != "screen.panic" || s.events[0].Kind != notify.KindScreen ||
		len(s.events[0].Image) == 0 || !strings.HasPrefix(s.events[0].Details, "[ 12.3] VFS") {
		t.Fatalf("events %+v", s.events)
	}
	// The same crash stays on the screen: reported once.
	s.picture = 100
	s.run(w, 20)
	if len(s.events) != 1 {
		t.Fatalf("reported again: %d", len(s.events))
	}
}

func TestScreenWatchNeedsCrashText(t *testing.T) {
	s := &watchedScreen{picture: 0, signal: true, verdict: screenVerdict{Class: "console", Score: 0.95},
		text: "debian login: \nPassword:"}
	w := s.watch()
	s.run(w, 4)
	if s.reads != 1 || len(s.events) != 0 {
		t.Fatalf("a login prompt: reads %d, events %v", s.reads, s.events)
	}
}

func TestScreenWatchReadsAtMostOnceInTheGap(t *testing.T) {
	// A console that keeps changing and settling: one read per gap.
	s := &watchedScreen{picture: 0, signal: true, verdict: screenVerdict{Class: "console", Score: 0.95},
		text: "server login:"}
	w := s.watch()
	for i := 0; i < 6; i++ { // two minutes: a change and a settle every 20 s
		s.picture = byte(i * 50)
		s.run(w, 2)
	}
	if s.reads != 1 {
		t.Fatalf("read %d times in one gap", s.reads)
	}
	// After the gap the waiting screen is read.
	s.run(w, 2)
	if s.reads != 2 {
		t.Fatalf("read %d times after the gap", s.reads)
	}
}

func TestScreenWatchLeavesAWatchedScreenAlone(t *testing.T) {
	for _, class := range []string{"console", "bsod"} {
		s := &watchedScreen{picture: 0, signal: true, viewers: 1, verdict: screenVerdict{Class: class, Score: 0.99},
			text: panicScreen}
		w := s.watch()
		s.run(w, 4)
		if s.reads != 0 || len(s.events) != 0 {
			t.Fatalf("%s with a viewer: reads %d, events %d", class, s.reads, len(s.events))
		}
	}
}

func TestScreenWatchStopScreen(t *testing.T) {
	s := &watchedScreen{picture: 0, signal: true, verdict: screenVerdict{Class: "bsod", Score: 0.85},
		text: ":(\nYour PC ran into a problem and needs to restart.\n20% complete\nStop code: CRITICAL_PROCESS_DIED"}
	w := s.watch()
	s.run(w, 4)
	if len(s.events) != 1 || s.events[0].Message != "screen.bsod" || !strings.Contains(s.events[0].Details, "CRITICAL_PROCESS_DIED") {
		t.Fatalf("events %+v", s.events)
	}
	// A blue wallpaper the classifier took for one: the OCR finds no crash.
	s = &watchedScreen{picture: 0, signal: true, verdict: screenVerdict{Class: "bsod", Score: 0.99}, text: "Recycle Bin\nThis PC"}
	w = s.watch()
	s.run(w, 4)
	if len(s.events) != 0 {
		t.Fatal("a desktop reported as a stop screen")
	}
	// The OCR failed: nothing is reported.
	s = &watchedScreen{picture: 0, signal: true, verdict: screenVerdict{Class: "bsod", Score: 0.99}, ocrErr: errors.New("busy")}
	w = s.watch()
	s.run(w, 4)
	if len(s.events) != 0 {
		t.Fatal("reported a stop screen without reading it")
	}
}

// A read that failed is tried again while the crash stays on the screen: at
// once when somebody else was reading, after the gap otherwise.
func TestScreenWatchRetriesAFailedRead(t *testing.T) {
	busy := screenTextError(-2, "the screen is being read")
	s := &watchedScreen{picture: 0, signal: true, verdict: screenVerdict{Class: "console", Score: 0.95},
		text: panicScreen, ocrErrs: []error{busy}}
	w := s.watch()
	s.run(w, 3)
	if s.reads != 2 || len(s.events) != 1 {
		t.Fatalf("after busy: reads %d, events %d", s.reads, len(s.events))
	}

	s = &watchedScreen{picture: 0, signal: true, verdict: screenVerdict{Class: "console", Score: 0.95},
		text: panicScreen, ocrErrs: []error{errors.New("capture timeout")}}
	w = s.watch()
	s.run(w, 3)
	if s.reads != 1 || len(s.events) != 0 {
		t.Fatalf("within the gap: reads %d, events %d", s.reads, len(s.events))
	}
	s.run(w, 30) // five minutes of the same panic
	if s.reads != 2 || len(s.events) != 1 {
		t.Fatalf("after the gap: reads %d, events %d", s.reads, len(s.events))
	}
}

// A viewer who comes while the OCR runs cancels it: the video waits until
// the read has ended, and nothing is reported.
func TestScreenWatchYieldsToAViewerDuringTheRead(t *testing.T) {
	s := &watchedScreen{picture: 0, signal: true, verdict: screenVerdict{Class: "console", Score: 0.95}, text: panicScreen}
	held := make(chan func(), 1)
	var cancelled bool
	s.duringRead = func(ctx context.Context) {
		go func() { release, _ := videogate.Hold(nil); held <- release }() // a video session starts
		select {
		case <-ctx.Done():
			cancelled = true
		case <-time.After(5 * time.Second):
		}
		select {
		case release := <-held:
			t.Error("the video went on before the read ended")
			held <- release
		case <-time.After(20 * time.Millisecond):
		}
	}
	w := s.watch()
	s.run(w, 2)
	release := <-held
	if !cancelled || s.reads != 1 || len(s.events) != 0 {
		t.Fatalf("cancelled %v, reads %d, events %d", cancelled, s.reads, len(s.events))
	}
	// While the video runs nothing is read.
	s.duringRead = nil
	s.picture = 100
	s.run(w, 2)
	s.picture = 0
	s.run(w, 2)
	if s.reads != 1 {
		t.Fatalf("read beside video: %d", s.reads)
	}
	release()
}

// The picture sent is the frame the OCR read, not the screen after it; a
// frame that differs from the screen judged is judged again.
func TestScreenWatchSendsTheFrameRead(t *testing.T) {
	s := &watchedScreen{picture: 20, signal: true, verdict: screenVerdict{Class: "console", Score: 0.95}, text: panicScreen}
	// The host restarts while the OCR runs: the desktop is up after it.
	s.duringRead = func(context.Context) { s.picture = 230 }
	w := s.watch()
	s.run(w, 2)
	if len(s.events) != 1 {
		t.Fatalf("events %d", len(s.events))
	}
	if v := imageValue(t, s.events[0].Image); v > 40 {
		t.Fatalf("the image is the screen after the read (%d)", v)
	}

	// The screen changed between the verdict and the OCR's frame.
	s = &watchedScreen{picture: 20, signal: true, verdict: screenVerdict{Class: "console", Score: 0.95}, text: panicScreen}
	w = s.watch()
	s.run(w, 1)
	classify := w.classify
	w.classify = func(ctx context.Context, planes []byte) (screenVerdict, error) {
		s.picture = 230 // after the verdict, before the read
		return classify(ctx, planes)
	}
	s.run(w, 1)
	if s.reads != 1 || len(s.events) != 0 {
		t.Fatalf("reported a frame other than the one judged: reads %d, events %d", s.reads, len(s.events))
	}
	if !w.pending {
		t.Fatal("the new screen is not judged")
	}
}

func TestScreenWatchOffWithoutSignalOrWish(t *testing.T) {
	s := &watchedScreen{picture: 0, signal: false, verdict: screenVerdict{Class: "bsod", Score: 0.99}}
	w := s.watch()
	s.run(w, 4)
	w.wants = func(notify.Kind) bool { return false }
	s.signal = true
	s.run(w, 4)
	if s.classify != 0 {
		t.Fatalf("classified %d times", s.classify)
	}
}

func TestCrashTextPatterns(t *testing.T) {
	for _, text := range []string{"Kernel panic - not syncing: Fatal exception", "BUG: kernel NULL pointer dereference",
		"Oops: 0000 [#1] SMP", "watchdog: BUG: soft lockup - CPU#1 stuck", "Call Trace:"} {
		if !screenPanicText.MatchString(text) {
			t.Errorf("not a panic: %q", text)
		}
	}
	for _, text := range []string{"Stop code: MEMORY_MANAGEMENT", "Your device ran into a problem and needs to restart.",
		"*** STOP: 0x0000007B", "IRQL_NOT_LESS_OR_EQUAL"} {
		if !screenBSODText.MatchString(text) {
			t.Errorf("not a stop screen: %q", text)
		}
	}
	for _, text := range []string{"debian login:", "Started OpenSSH server.", "Recycle Bin", "README_FIRST"} {
		if screenPanicText.MatchString(text) || screenBSODText.MatchString(text) {
			t.Errorf("a crash: %q", text)
		}
	}
}

func TestShrinkPlanes(t *testing.T) {
	// 4x2, B plane 0..7, G all 100, R all 200; to 2x1: block means.
	planes := []byte{0, 1, 2, 3, 4, 5, 6, 7, 100, 100, 100, 100, 100, 100, 100, 100, 200, 200, 200, 200, 200, 200, 200, 200}
	got := shrinkPlanes(planes, 4, 2, 2, 1)
	want := []byte{(0 + 1 + 4 + 5) / 4, (2 + 3 + 6 + 7) / 4, 100, 100, 200, 200}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	// Full HD to the classifier's size, odd blocks included.
	big := bytes.Repeat([]byte{9}, 1920*1080*3)
	small := shrinkPlanes(big, 1920, 1080, screenThumbWidth, screenThumbHeight)
	if len(small) != screenThumbWidth*screenThumbHeight*3 || bytes.IndexFunc(small, func(r rune) bool { return r != 9 }) >= 0 {
		t.Fatal("a flat picture did not stay flat")
	}
}

func TestPlanesJPEG(t *testing.T) {
	planes := make([]byte, 4*2*3)
	for i := 0; i < 8; i++ {
		planes[16+i] = 255 // R plane: red
	}
	data := planesJPEG(planes, 4, 2)
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := img.At(1, 1).RGBA()
	if r>>8 < 200 || g>>8 > 60 || b>>8 > 60 {
		t.Fatalf("colour %d %d %d", r>>8, g>>8, b>>8)
	}
}
