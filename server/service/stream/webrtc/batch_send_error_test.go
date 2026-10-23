package webrtc

import (
	"NanoKVM-Server/service/stream"
	"errors"
	"syscall"
	"testing"

	"github.com/pion/webrtc/v4"
)

type failingBatcher struct{ err error }

func (failingBatcher) BeginFrame()       {}
func (b failingBatcher) EndFrame() error { return b.err }

func frameClient(t *testing.T, batchErr error) *Client {
	t.Helper()
	track, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264}, "video", "test")
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{
		pathMTU:    newPeerPathMTU(),
		packetizer: newAdaptiveVideoPacketizer(stream.VideoCodecH264),
		track:      &Track{video: track},
	}
	client.pathMTU.setBatcher(failingBatcher{err: batchErr})
	return client
}

// A frame the batched socket could not send is dropped, and the viewer kept:
// without batching Pion drops such packets the same way.
func TestABatchSendErrorKeepsThePeer(t *testing.T) {
	frame := stream.VideoFrame{Result: 3, Timestamp: 1, Data: []byte{0, 0, 0, 1, 0x65, 1, 2, 3}}
	for _, err := range []error{syscall.ENETUNREACH, syscall.EHOSTUNREACH, syscall.EPERM, syscall.EMSGSIZE} {
		client := frameClient(t, err)
		if got := client.writeFrame(frame); got != nil {
			t.Fatalf("%v ended the peer: %v", err, got)
		}
		if client.sendErrors.last.IsZero() {
			t.Fatalf("%v was not counted", err)
		}
	}
	if err := frameClient(t, nil).writeFrame(frame); err != nil {
		t.Fatal(err)
	}
}

// The log line is rate-limited: a flapping route does not flood the log.
func TestDroppedFramesAreLoggedAtMostEveryTenSeconds(t *testing.T) {
	var l sendErrorLog
	for range 100 {
		l.note(errors.New("sendmmsg: network is unreachable"))
	}
	if l.dropped != 99 {
		t.Fatalf("%d dropped frames waiting for the next line, want 99", l.dropped)
	}
}
