package stream

import (
	"NanoKVM-Server/common"
	"NanoKVM-Server/internal/videogate"
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestVideoSourceSharesOnlyExactConfiguration(t *testing.T) {
	source := newVideoSource(func(EncoderConfig) ([]byte, []byte, int) { return nil, nil, 0 })
	h265 := DefaultEncoderConfig()
	h264 := h265
	h264.Codec = VideoCodecH264

	first, err := source.subscribe(h264)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	second, err := source.subscribe(h264)
	if err != nil {
		t.Fatalf("exact configuration was rejected: %v", err)
	}
	defer second.Close()
	if first.session != second.session {
		t.Fatal("exactly matching subscribers did not share the encoder session")
	}

	_, err = source.subscribe(h265)
	var conflict *EncoderConfigConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("mismatched configuration error = %v, want EncoderConfigConflictError", err)
	}
	if conflict.Active != h264 || conflict.Requested != h265 {
		t.Fatalf("conflict = %+v, want active=%+v requested=%+v", conflict, h264, h265)
	}
}

func TestAutomaticVideoSubscriptionUsesSharedSelection(t *testing.T) {
	for _, config := range []EncoderConfig{LegacyEncoderConfig(), DefaultEncoderConfig()} {
		source := newVideoSource(func(EncoderConfig) ([]byte, []byte, int) { return nil, nil, 0 })
		source.selectConfig(config)
		first, err := source.subscribe(EncoderConfig{})
		if err != nil || first.Config() != config {
			t.Fatalf("idle selection: %v %v", first, err)
		}
		second, err := source.subscribe(EncoderConfig{})
		if err != nil || second.session != first.session || second.Config() != config {
			t.Fatalf("active selection: %v %v", second, err)
		}
		second.Close()
		first.Close()
	}
}

func TestAutomaticVideoSubscriptionAdoptsActiveCodecOrDefault(t *testing.T) {
	source := newVideoSource(func(EncoderConfig) ([]byte, []byte, int) { return nil, nil, 0 })
	first, err := source.subscribe(LegacyEncoderConfig())
	if err != nil {
		t.Fatal(err)
	}
	second, err := source.subscribe(EncoderConfig{})
	if err != nil || second.Config() != LegacyEncoderConfig() || first.session != second.session {
		t.Fatalf("active codec was not shared: %v %v", second, err)
	}
	second.Close()
	first.Close()
	third, err := source.subscribe(EncoderConfig{})
	if err != nil || third.Config() != DefaultEncoderConfig() {
		t.Fatalf("drained session overrode default: %v %v", third, err)
	}
	third.Close()
}

func TestAutomaticVideoSubscriptionResolvesAgainAfterDrain(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	source := newVideoSource(func(EncoderConfig) ([]byte, []byte, int) {
		once.Do(func() { close(started) })
		<-release
		return nil, nil, 0
	})
	old, err := source.subscribe(DefaultEncoderConfig())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("capture did not start")
	}
	old.Close()
	result := make(chan *VideoSubscription, 1)
	go func() {
		sub, err := source.subscribe(EncoderConfig{})
		if err != nil {
			result <- nil
		} else {
			result <- sub
		}
	}()
	select {
	case <-result:
		t.Fatal("automatic join did not wait for draining native capture")
	case <-time.After(30 * time.Millisecond):
	}
	source.selectConfig(LegacyEncoderConfig())
	close(release)
	select {
	case sub := <-result:
		if sub == nil || sub.Config() != LegacyEncoderConfig() {
			t.Fatalf("automatic join used stale selection: %v", sub)
		}
		sub.Close()
	case <-time.After(time.Second):
		t.Fatal("automatic join failed after drain")
	}
}

func TestAutomaticVideoSubscriptionValidatesResolvedCodecBeforeCapture(t *testing.T) {
	source := newVideoSource(func(EncoderConfig) ([]byte, []byte, int) {
		t.Error("rejected subscription started capture")
		return nil, nil, 0
	})
	source.selectConfig(LegacyEncoderConfig())
	rejected := errors.New("unsupported resolution")
	_, err := source.subscribeValidated(EncoderConfig{}, func(config EncoderConfig) error {
		if config != LegacyEncoderConfig() {
			t.Errorf("validation saw unresolved codec: %+v", config)
		}
		return rejected
	})
	if err != rejected || source.session != nil || len(source.subscribers) != 0 {
		t.Fatalf("invalid automatic subscription was activated: %v", err)
	}
}

func TestVideoSourceDoesNotLeakFramesAcrossSessions(t *testing.T) {
	source := newVideoSource(func(EncoderConfig) ([]byte, []byte, int) { return nil, nil, 0 })
	h265 := DefaultEncoderConfig()
	h264 := h265
	h264.Codec = VideoCodecH264

	oldSubscription, err := source.subscribe(h264)
	if err != nil {
		t.Fatal(err)
	}
	oldSession := oldSubscription.session
	oldSubscription.Close()

	newSubscription, err := source.subscribe(h265)
	if err != nil {
		t.Fatal(err)
	}
	defer newSubscription.Close()
	if oldSession == newSubscription.session {
		t.Fatal("encoder session was reused after its last subscriber left")
	}
	if subscribers := source.snapshot(oldSession); len(subscribers) != 0 {
		t.Fatalf("old session can see %d subscriber(s) from the new session", len(subscribers))
	}
}

// A RustDesk session is not a counted viewer; its session must hold the
// capture from start to end, or capture parked without viewers stays resumed.
func TestVideoSessionHoldsCaptureWhileItRuns(t *testing.T) {
	source := newVideoSource(func(EncoderConfig) ([]byte, []byte, int) { return nil, nil, 0 })
	var held, released atomic.Int32
	hold := func() func() {
		held.Add(1)
		return func() { released.Add(1) }
	}
	source.holdCapture.Store(&hold)

	first, err := source.subscribe(DefaultEncoderConfig())
	if err != nil {
		t.Fatal(err)
	}
	second, err := source.subscribe(DefaultEncoderConfig())
	if err != nil {
		t.Fatal(err)
	}
	session := first.session
	waitFor(t, func() bool { return held.Load() == 1 })
	first.Close()
	if released.Load() != 0 {
		t.Fatal("capture released while a subscriber remains")
	}
	second.Close()
	<-session.done
	if held.Load() != 1 || released.Load() != 1 {
		t.Fatalf("held %d, released %d; want one hold for the session", held.Load(), released.Load())
	}
}

// A video session wakes the capture (its lease) only after a background OCR
// read it cancelled has ended; a session given up while waiting takes none.
func TestVideoSessionWaitsForBackgroundOCR(t *testing.T) {
	source := newVideoSource(func(EncoderConfig) ([]byte, []byte, int) { return nil, nil, 0 })
	var ocrEnded atomic.Bool
	woke := make(chan bool, 4)
	hold := func() func() {
		woke <- ocrEnded.Load()
		return func() {}
	}
	source.holdCapture.Store(&hold)
	ctx, end, ok := videogate.Begin(context.Background())
	if !ok {
		t.Fatal("background read refused")
	}
	go func() {
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond) // the helper exits, its memory is freed
		ocrEnded.Store(true)
		end()
	}()
	subscription, err := source.subscribe(DefaultEncoderConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !<-woke {
		t.Fatal("the capture woke beside the OCR")
	}
	session := subscription.session
	subscription.Close()
	<-session.done

	// A session whose viewer leaves while the OCR has not ended yet.
	ctx, end, _ = videogate.Begin(context.Background())
	subscription, err = source.subscribe(DefaultEncoderConfig())
	if err != nil {
		t.Fatal(err)
	}
	<-ctx.Done()
	session = subscription.session
	subscription.Close()
	<-session.done
	end()
	select {
	case <-woke:
		t.Fatal("a session given up woke the capture")
	default:
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestVideoSourceWaitsForPreviousCaptureBeforeStartingNewProfile(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	source := newVideoSource(func(EncoderConfig) ([]byte, []byte, int) {
		once.Do(func() { close(started) })
		<-release
		return nil, nil, 0
	})
	h265 := DefaultEncoderConfig()
	h264 := h265
	h264.Codec = VideoCodecH264

	oldSubscription, err := source.subscribe(h264)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("old capture did not start")
	}
	oldSubscription.Close()

	type subscribeResult struct {
		subscription *VideoSubscription
		err          error
	}
	result := make(chan subscribeResult, 1)
	go func() {
		subscription, subscribeErr := source.subscribe(h265)
		result <- subscribeResult{subscription: subscription, err: subscribeErr}
	}()

	select {
	case <-result:
		t.Fatal("new profile started before the previous native capture returned")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)

	select {
	case got := <-result:
		if got.err != nil {
			t.Fatal(got.err)
		}
		got.subscription.Close()
	case <-time.After(time.Second):
		t.Fatal("new profile did not start after the previous capture returned")
	}
}

func TestVideoSubscriptionRecoversAtKeyframeAfterBackpressure(t *testing.T) {
	subscription := &VideoSubscription{
		source: &VideoSource{},
		frames: make(chan VideoFrame, 4),
		done:   make(chan struct{}),
	}

	for i := 0; i < cap(subscription.frames); i++ {
		if !subscription.send(VideoFrame{Result: 2, Timestamp: int64(i)}) {
			t.Fatalf("frame %d was unexpectedly rejected", i)
		}
	}
	if subscription.send(VideoFrame{Result: 2, Timestamp: 4}) {
		t.Fatal("overflowing delta frame was unexpectedly accepted")
	}
	if !subscription.source.keyframeRequested.Load() {
		t.Fatal("overflow did not request encoder refresh")
	}
	if !subscription.waitingForKeyframe {
		t.Fatal("subscription did not enter keyframe recovery after overflow")
	}
	if len(subscription.frames) != 0 {
		t.Fatalf("stale queue contains %d frame(s) after overflow", len(subscription.frames))
	}
	if subscription.send(VideoFrame{Result: 2, Timestamp: 5}) {
		t.Fatal("delta frame was accepted while waiting for a keyframe")
	}
	if !subscription.send(VideoFrame{Result: keyFrameResult, Timestamp: 6}) {
		t.Fatal("recovery keyframe was rejected")
	}

	frame := <-subscription.frames
	if frame.Result != keyFrameResult || frame.Timestamp != 6 {
		t.Fatalf("recovery frame = %+v, want keyframe timestamp 6", frame)
	}
}

func TestNativeEncoderMappings(t *testing.T) {
	if got := VideoCodecH264.NativeCodec(); got != 1 {
		t.Fatalf("H.264 native codec = %d, want 1", got)
	}
	if got := VideoCodecH265.NativeCodec(); got != 2 {
		t.Fatalf("H.265 native codec = %d, want 2", got)
	}
}

func TestVideoSubscriptionStartsAtKeyframeAndPreservesErrors(t *testing.T) {
	source := newVideoSource(func(EncoderConfig) ([]byte, []byte, int) { return nil, nil, 0 })
	config := DefaultEncoderConfig()
	config.Codec = VideoCodecH264
	first, err := source.subscribe(config)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := source.subscribe(config)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	for _, subscription := range []*VideoSubscription{first, second} {
		if subscription.send(VideoFrame{Result: 2, Timestamp: 1}) {
			t.Fatal("new decoder received a delta frame before its keyframe")
		}
		if !subscription.send(VideoFrame{Result: -1, Timestamp: 2}) {
			t.Fatal("native error hidden while waiting for the initial keyframe")
		}
		if got := <-subscription.frames; got.Result != -1 {
			t.Fatalf("error frame = %+v", got)
		}
		if subscription.send(VideoFrame{Result: 2, Timestamp: 3}) {
			t.Fatal("native error incorrectly ended keyframe wait")
		}
		if !subscription.send(VideoFrame{Result: keyFrameResult, Timestamp: 4}) || !subscription.send(VideoFrame{Result: 2, Timestamp: 5}) {
			t.Fatal("keyframe and following delta frame were not delivered")
		}
		if got := <-subscription.frames; got.Result != keyFrameResult || got.Timestamp != 4 {
			t.Fatalf("first video frame = %+v", got)
		}
		if got := <-subscription.frames; got.Result != 2 || got.Timestamp != 5 {
			t.Fatalf("following frame = %+v", got)
		}
	}
}

func TestActiveEncoderConfigExcludesDrainingCapture(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	source := newVideoSource(func(EncoderConfig) ([]byte, []byte, int) {
		once.Do(func() { close(started) })
		<-release
		return nil, nil, 0
	})
	defer close(release)
	if _, active := source.activeConfig(); active {
		t.Fatal("idle source is active")
	}
	first, err := source.subscribe(LegacyEncoderConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("capture did not start")
	}
	if config, active := source.activeConfig(); !active || config != LegacyEncoderConfig() {
		t.Fatalf("active config = %+v, active=%t", config, active)
	}
	second, err := source.subscribe(LegacyEncoderConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	first.Close()
	if _, active := source.activeConfig(); !active {
		t.Fatal("lost remaining subscriber")
	}
	second.Close()
	if _, active := source.activeConfig(); active {
		t.Fatal("draining session advertised as active")
	}
}

// H.264 runs up to 2560x1440 of encoded picture (1080x1920 in portrait); the
// stream limit counts, H.265 is never refused.
func TestH264CodecPolicy(t *testing.T) {
	for _, c := range []struct {
		limit         uint16
		width, height int
		blocked       bool
	}{
		{0, 3840, 2160, true},
		{2160, 3840, 2160, true},
		{1440, 3840, 2160, false},
		{1080, 3840, 2160, false},
		{720, 3840, 2160, false},
		{600, 3840, 2160, false},
		{0, 2560, 1440, false},
		{2160, 2560, 1440, false},
		{1440, 2560, 1440, false},
		{0, 1920, 1080, false},
		{2160, 1920, 1080, false},
		{0, 3440, 1440, true},
		{1440, 3440, 1440, false},
		// Portrait: above 1080x1920 it needs a limit of 1080 or lower.
		{0, 1440, 2560, true},
		{2160, 1440, 2560, true},
		{1440, 1440, 2560, true},
		{1080, 1440, 2560, false},
		{720, 1440, 2560, false},
		{0, 1088, 1920, false},
		{0, 1080, 1920, false},
		{0, 720, 1280, false},
		// No signal: nothing is encoded yet.
		{0, 0, 0, false},
	} {
		if got := h264Blocked(VideoCodecH264, c.limit, c.width, c.height); got != c.blocked {
			t.Errorf("H.264 at %dx%d, limit %d: blocked %v, want %v", c.width, c.height, c.limit, got, c.blocked)
		}
		if h264Blocked(VideoCodecH265, c.limit, c.width, c.height) {
			t.Errorf("H.265 at %dx%d, limit %d blocked", c.width, c.height, c.limit)
		}
	}
}

func TestSubscribeRefusesH264At4K(t *testing.T) {
	previousInput, previousLimit := captureInput, common.GetScreen().Height
	t.Cleanup(func() { captureInput = previousInput; common.SetScreen("resolution", int(previousLimit)) })
	captureInput = func() (int, int) { return 3840, 2160 }
	common.SetScreen("resolution", 0)
	if err := validateVideoConfig(EncoderConfig{Codec: VideoCodecH264}); err == nil || err.Error() != common.H264BlockedMessage {
		t.Fatalf("H.264 at 3840x2160: %v", err)
	}
	if err := validateVideoConfig(EncoderConfig{Codec: VideoCodecH265}); err != nil {
		t.Fatalf("H.265 at 3840x2160: %v", err)
	}
	common.SetScreen("resolution", 1440)
	if err := validateVideoConfig(EncoderConfig{Codec: VideoCodecH264}); err != nil {
		t.Fatalf("H.264 at 3840x2160 limited to 1440: %v", err)
	}
}

func TestSelectedH264FallsBackToH265At4K(t *testing.T) {
	previousInput, previousLimit := captureInput, common.GetScreen().Height
	t.Cleanup(func() { captureInput = previousInput; common.SetScreen("resolution", int(previousLimit)) })
	input := [2]int{3840, 2160}
	captureInput = func() (int, int) { return input[0], input[1] }
	common.SetScreen("resolution", 0)

	source := newVideoSource(func(EncoderConfig) ([]byte, []byte, int) { return nil, nil, 0 })
	source.selectConfig(LegacyEncoderConfig())
	if config, selected := source.selectedConfig(); !selected || config != DefaultEncoderConfig() {
		t.Fatalf("selected H.264 at 3840x2160 reports %+v", config)
	}
	viewer, err := source.subscribeValidated(EncoderConfig{}, validateVideoConfig)
	if err != nil || viewer.Config() != DefaultEncoderConfig() {
		t.Fatalf("automatic join at 3840x2160: %+v %v", viewer, err)
	}
	if _, err := source.subscribeValidated(LegacyEncoderConfig(), validateVideoConfig); err == nil {
		t.Fatal("explicit H.264 joined at 3840x2160")
	}

	// The input shrinks to QHD: the running H.265 session keeps its viewers
	// and new ones join it; the next session uses the selected H.264.
	input = [2]int{2560, 1440}
	second, err := source.subscribeValidated(EncoderConfig{}, validateVideoConfig)
	if err != nil || second.Config() != DefaultEncoderConfig() {
		t.Fatalf("join during the H.265 fallback session: %+v %v", second, err)
	}
	viewer.Close()
	second.Close()
	<-viewer.session.done
	next, err := source.subscribeValidated(EncoderConfig{}, validateVideoConfig)
	if err != nil || next.Config() != LegacyEncoderConfig() {
		t.Fatalf("selected H.264 at 2560x1440: %+v %v", next, err)
	}
	next.Close()
}

func TestH264SessionEndsWhenThePictureGrowsPastQHD(t *testing.T) {
	previousInput, previousLimit := captureInput, common.GetScreen().Height
	t.Cleanup(func() { captureInput = previousInput; common.SetScreen("resolution", int(previousLimit)) })
	var input atomic.Int64
	input.Store(2560<<16 | 1440)
	captureInput = func() (int, int) { v := input.Load(); return int(v >> 16), int(v & 0xffff) }
	common.SetScreen("resolution", 0)

	source := newVideoSource(func(EncoderConfig) ([]byte, []byte, int) { return nil, nil, -1 })
	viewer, err := source.subscribeValidated(LegacyEncoderConfig(), validateVideoConfig)
	if err != nil {
		t.Fatal(err)
	}
	input.Store(3840<<16 | 2160)
	select {
	case <-viewer.done:
	case <-time.After(2 * time.Second):
		t.Fatal("H.264 viewer kept a session that cannot encode 3840x2160")
	}
	<-viewer.session.done
}

func TestVideoSourceSnapshotRemainsImmutableAcrossMembershipChanges(t *testing.T) {
	source := newVideoSource(func(EncoderConfig) ([]byte, []byte, int) { return nil, nil, 0 })
	first, err := source.subscribe(DefaultEncoderConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	old := source.snapshot(first.session)
	second, err := source.subscribe(DefaultEncoderConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if len(old) != 1 || old[0] != first {
		t.Fatal("joining changed an in-flight snapshot")
	}
	joined := source.snapshot(first.session)
	if len(joined) != 2 {
		t.Fatal("new subscriber absent")
	}
	first.Close()
	current := source.snapshot(second.session)
	if len(current) != 1 || current[0] != second {
		t.Fatal("closed subscriber retained")
	}
	if len(joined) != 2 || joined[0] == joined[1] {
		t.Fatal("removal changed an in-flight snapshot")
	}
	if old[0].send(VideoFrame{Result: keyFrameResult}) {
		t.Fatal("closed subscriber accepted a stale snapshot frame")
	}
	if allocations := testing.AllocsPerRun(100, func() { source.snapshot(second.session) }); allocations != 0 {
		t.Fatalf("per-frame subscriber snapshot allocates: %v", allocations)
	}
}

func TestCaptureDeadlinePreservesCadenceAndBoundsCatchup(t *testing.T) {
	start := time.Unix(0, 0)
	period := time.Second / 75
	for _, tc := range []struct {
		name      string
		now, want time.Duration
	}{
		{"on time", 0, period},
		{"small delay retains phase", 2 * time.Millisecond, period},
		{"one missed period catches up", period + time.Millisecond, period},
		{"long stall drops stale work", 10 * period, 10 * period},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := advanceCaptureDeadline(start, start.Add(tc.now), period)
			if !got.Equal(start.Add(tc.want)) {
				t.Fatalf("deadline=%v want=%v", got, start.Add(tc.want))
			}
		})
	}
}

func TestKeyframeRequestsCoalesceWithoutLosingDeferredIntent(t *testing.T) {
	s := &VideoSource{}
	now := time.Unix(1000, 0)
	var last time.Time
	if s.takeKeyframeRequest(now, &last) {
		t.Fatal("unsolicited refresh")
	}
	for i := 0; i < 100; i++ {
		s.keyframeRequested.Store(true)
	}
	if !s.takeKeyframeRequest(now, &last) {
		t.Fatal("first request lost")
	}
	if s.takeKeyframeRequest(now.Add(time.Second), &last) {
		t.Fatal("coalesced request repeated")
	}
	s.keyframeRequested.Store(true)
	if s.takeKeyframeRequest(now.Add(499*time.Millisecond), &last) {
		t.Fatal("rate limit ignored")
	}
	if !s.takeKeyframeRequest(now.Add(500*time.Millisecond), &last) {
		t.Fatal("deferred request lost")
	}
}

func TestExplicitCodecSelectionReplacesSharedSession(t *testing.T) {
	source := newVideoSource(func(EncoderConfig) ([]byte, []byte, int) { return nil, nil, 0 })
	old, err := source.subscribe(LegacyEncoderConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	source.selectConfig(DefaultEncoderConfig())
	select {
	case <-old.done:
	default:
		t.Fatal("old subscriber was not retired")
	}
	config, selected := source.selectedConfig()
	if !selected || config != DefaultEncoderConfig() {
		t.Fatal("new selection not visible while draining")
	}
	if stale, err := source.subscribe(LegacyEncoderConfig()); err == nil {
		stale.Close()
		t.Fatal("stale viewer reclaimed the encoder")
	}
	next, err := source.subscribe(DefaultEncoderConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if next.session == old.session {
		t.Fatal("codec change reused old capture session")
	}
}

func TestSelectingCurrentCodecDoesNotInterruptViewers(t *testing.T) {
	source := newVideoSource(func(EncoderConfig) ([]byte, []byte, int) { return nil, nil, 0 })
	first, _ := source.subscribe(DefaultEncoderConfig())
	defer first.Close()
	source.selectConfig(DefaultEncoderConfig())
	select {
	case <-first.done:
		t.Fatal("same codec interrupted stream")
	default:
	}
	second, err := source.subscribe(DefaultEncoderConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if second.session != first.session {
		t.Fatal("matching viewer not shared")
	}
}

// The native reader can consume the whole cadence period. Workers created at
// its return must run before another blocking capture, including empty/errors.
func TestLateVideoCaptureAllowsNewInputWorkersToRun(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	for _, result := range []int{-1, 0} {
		var workers atomic.Int32
		captures := 0
		finished := make(chan int32, 1)
		source := newVideoSource(func(EncoderConfig) ([]byte, []byte, int) {
			captures++
			if captures == 1 {
				time.Sleep(50 * time.Millisecond)
				go func() { workers.Add(1) }()
				go func() { workers.Add(1) }()
			} else if captures == 2 {
				finished <- workers.Load()
			}
			return nil, nil, result
		})
		subscription, err := source.subscribe(DefaultEncoderConfig())
		if err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-finished:
			subscription.Close()
			<-subscription.session.done
			if got != 2 {
				t.Fatalf("capture result %d: only %d new input workers ran before next capture", result, got)
			}
		case <-time.After(time.Second):
			subscription.Close()
			t.Fatal("capture did not progress")
		}
	}
}

func (s *VideoSource) subscribe(config EncoderConfig) (*VideoSubscription, error) {
	return s.subscribeValidated(config, nil)
}
