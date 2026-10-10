package stream

import (
	"NanoKVM-Server/common"
	"bytes"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeVideoStream stands in for the native-paced capture thread.
type fakeVideoStream struct {
	frames    chan VideoFrame // closing it ends the stream without Stop
	stopped   chan struct{}
	stopOnce  sync.Once
	closed    chan struct{}
	closeOnce sync.Once
	release   chan struct{} // when set, Close waits for it like a read in flight
	updates   chan common.VideoCaptureParams
	headroom  atomic.Int32
}

func newFakeVideoStream() *fakeVideoStream {
	return &fakeVideoStream{
		frames:  make(chan VideoFrame, 8),
		stopped: make(chan struct{}),
		closed:  make(chan struct{}),
		updates: make(chan common.VideoCaptureParams, 8),
	}
}

func (f *fakeVideoStream) Update(params common.VideoCaptureParams) { f.updates <- params }

func (f *fakeVideoStream) Next(headroom int) ([]byte, []byte, int, bool) {
	f.headroom.Store(int32(headroom))
	select {
	case frame, ok := <-f.frames:
		if !ok {
			return nil, nil, -1, false
		}
		return frame.Storage, frame.Data, frame.Result, true
	case <-f.stopped:
		return nil, nil, -1, false
	}
}

func (f *fakeVideoStream) Stop() { f.stopOnce.Do(func() { close(f.stopped) }) }

func (f *fakeVideoStream) Close() {
	f.closeOnce.Do(func() {
		if f.release != nil {
			<-f.release
		}
		close(f.closed)
	})
}

func newStreamVideoSource(t *testing.T, stream *fakeVideoStream, captureFrame func(EncoderConfig) ([]byte, []byte, int)) (*VideoSource, chan common.VideoCaptureParams) {
	started := make(chan common.VideoCaptureParams, 4)
	if captureFrame == nil {
		captureFrame = func(EncoderConfig) ([]byte, []byte, int) {
			t.Error("native-paced session used the Go-paced read")
			time.Sleep(10 * time.Millisecond)
			return nil, nil, -1
		}
	}
	source := newVideoSource(captureFrame)
	source.startStream = func(params common.VideoCaptureParams) common.VideoStream {
		started <- params
		return stream
	}
	return source, started
}

func nextFrame(t *testing.T, subscription *VideoSubscription) VideoFrame {
	t.Helper()
	select {
	case frame := <-subscription.frames:
		return frame
	case <-time.After(time.Second):
		t.Fatal("no frame")
		return VideoFrame{}
	}
}

func TestNativePacedSessionDeliversFramesAndClosesStream(t *testing.T) {
	stream := newFakeVideoStream()
	source, started := newStreamVideoSource(t, stream, nil)
	subscription, err := source.subscribe(DefaultEncoderConfig())
	if err != nil {
		t.Fatal(err)
	}
	params := <-started
	if params.Codec != uint8(DefaultEncoderConfig().Codec.NativeCodec()) || params.FPS == 0 {
		t.Fatalf("start parameters %+v", params)
	}

	stream.frames <- VideoFrame{Data: []byte{1}, Storage: []byte{0, 1}, Result: 4}
	stream.frames <- VideoFrame{Result: -4}
	stream.frames <- VideoFrame{Data: []byte{2}, Storage: []byte{0, 2}, Result: keyFrameResult}
	// A new decoder starts at a keyframe; errors stay observable.
	if frame := nextFrame(t, subscription); frame.Result != -4 {
		t.Fatalf("error frame %+v", frame)
	}
	frame := nextFrame(t, subscription)
	if !frame.IsKeyframe() || !bytes.Equal(frame.Data, []byte{2}) || frame.Duration != time.Second/time.Duration(params.FPS) {
		t.Fatalf("key frame %+v", frame)
	}
	if stream.headroom.Load() != videoHeadroom {
		t.Fatalf("headroom %d", stream.headroom.Load())
	}

	session := subscription.session
	subscription.Close()
	select {
	case <-session.done:
	case <-time.After(time.Second):
		t.Fatal("session did not end")
	}
	select {
	case <-stream.closed:
	default:
		t.Fatal("session ended before its stream was closed")
	}
}

func TestNativePacedSessionWaitsForStreamCloseBeforeNewProfile(t *testing.T) {
	stream := newFakeVideoStream()
	stream.release = make(chan struct{})
	source, started := newStreamVideoSource(t, stream, nil)
	h264 := DefaultEncoderConfig()
	h264.Codec = VideoCodecH264
	old, err := source.subscribe(h264)
	if err != nil {
		t.Fatal(err)
	}
	<-started
	old.Close()

	result := make(chan *VideoSubscription, 1)
	go func() {
		subscription, _ := source.subscribe(DefaultEncoderConfig())
		result <- subscription
	}()
	select {
	case <-result:
		t.Fatal("new profile started before the previous stream closed")
	case <-time.After(50 * time.Millisecond):
	}
	close(stream.release)
	select {
	case subscription := <-result:
		if subscription == nil {
			t.Fatal("new profile rejected")
		}
		subscription.Close()
	case <-time.After(time.Second):
		t.Fatal("new profile did not start")
	}
}

func TestNativePacedSessionFallsBackToGoPacedReads(t *testing.T) {
	stream := newFakeVideoStream()
	read := make(chan struct{}, 1)
	source, _ := newStreamVideoSource(t, stream, func(EncoderConfig) ([]byte, []byte, int) {
		select {
		case read <- struct{}{}:
		default:
		}
		return nil, nil, -1
	})
	subscription, err := source.subscribe(DefaultEncoderConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	close(stream.frames) // e.g. vision closed or the worker failed
	select {
	case <-read:
	case <-time.After(time.Second):
		t.Fatal("Go-paced loop did not take over")
	}
	select {
	case <-stream.closed:
	case <-time.After(time.Second):
		t.Fatal("ended stream was not closed")
	}
}

func TestNativePacedSessionPublishesScreenChanges(t *testing.T) {
	previous := common.GetScreen().GOP
	defer common.SetScreen("gop", int(previous))
	stream := newFakeVideoStream()
	source, started := newStreamVideoSource(t, stream, nil)
	subscription, err := source.subscribe(DefaultEncoderConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	<-started

	gop := 7
	if previous == 7 {
		gop = 8
	}
	common.SetScreen("gop", gop)
	stream.frames <- VideoFrame{Result: -1}
	select {
	case params := <-stream.updates:
		if params.GOP != uint8(gop) {
			t.Fatalf("published GOP %d", params.GOP)
		}
	case <-time.After(time.Second):
		t.Fatal("screen change was not published")
	}
}
