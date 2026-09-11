package stream

import (
	"errors"
	"sync"
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
