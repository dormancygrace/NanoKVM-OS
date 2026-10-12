package webrtc

import (
	"context"
	"testing"
	"time"

	"NanoKVM-Server/service/stream/audio"

	"github.com/pion/interceptor"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

type sentPacket struct {
	ssrc      uint32
	seq       uint16
	timestamp uint32
	at        time.Time
}

type recordingWriter struct{ sent chan sentPacket }

func (w *recordingWriter) WriteRTP(h *rtp.Header, payload []byte) (int, error) {
	w.sent <- sentPacket{ssrc: h.SSRC, seq: h.SequenceNumber, timestamp: h.Timestamp, at: time.Now()}
	return len(payload), nil
}

func (w *recordingWriter) Write(b []byte) (int, error) { return len(b), nil }

// trackBinding stands in for the peer connection a track is bound to.
type trackBinding struct{ writer *recordingWriter }

func (b trackBinding) CodecParameters() []webrtc.RTPCodecParameters {
	return []webrtc.RTPCodecParameters{{RTPCodecCapability: opusCapability(), PayloadType: opusPayloadType}}
}
func (trackBinding) HeaderExtensions() []webrtc.RTPHeaderExtensionParameter { return nil }
func (trackBinding) SSRC() webrtc.SSRC                                      { return 0x5eed }
func (trackBinding) SSRCRetransmission() webrtc.SSRC                        { return 0 }
func (trackBinding) SSRCForwardErrorCorrection() webrtc.SSRC                { return 0 }
func (b trackBinding) WriteStream() webrtc.TrackLocalWriter                 { return b.writer }
func (trackBinding) ID() string                                             { return "timeline-test" }
func (trackBinding) RTCPReader() interceptor.RTCPReader                     { return nil }

// fakeCapture feeds one listening period with packets from the test.
type fakeCapture struct{ packets chan audio.Packet }

func (f *fakeCapture) Read(ctx context.Context) (audio.Packet, error) {
	select {
	case p := <-f.packets:
		return p, nil
	case <-ctx.Done():
		return audio.Packet{}, ctx.Err()
	}
}

func (f *fakeCapture) Close() {}

// After audio-stop and audio-start the track keeps its SSRC and sequence,
// and its RTP clock covers the muted interval, whether the capture went on
// (higher index) or restarted (index from 1).
func TestAudioTimelineSurvivesStopStart(t *testing.T) {
	writer := &recordingWriter{sent: make(chan sentPacket, 16)}
	track, err := webrtc.NewTrackLocalStaticSample(opusCapability(), "audio", mediaStreamID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := track.Bind(trackBinding{writer: writer}); err != nil {
		t.Fatal(err)
	}
	capture := &fakeCapture{packets: make(chan audio.Packet, 4)}
	old := subscribeAudio
	subscribeAudio = func() (packetSource, error) { return capture, nil }
	t.Cleanup(func() { subscribeAudio = old })

	client := &Client{}
	client.audio.track = track
	listen := func(index uint64) sentPacket {
		t.Helper()
		if err := client.startAudio(); err != nil {
			t.Fatal(err)
		}
		capture.packets <- audio.Packet{Data: []byte{0xfc, 0xff, 0xfe}, Index: index}
		select {
		case p := <-writer.sent:
			client.stopAudio()
			return p
		case <-time.After(2 * time.Second):
			t.Fatal("no packet sent")
			return sentPacket{}
		}
	}
	ticks := func(d time.Duration) int64 { return d.Microseconds() * 48 / 1000 }

	first := listen(10)
	for _, c := range []struct {
		name  string
		pause time.Duration
		index uint64
	}{
		{"capture continued", 200 * time.Millisecond, 21},
		{"capture restarted", 150 * time.Millisecond, 1},
	} {
		time.Sleep(c.pause)
		next := listen(c.index)
		if next.ssrc != first.ssrc {
			t.Fatalf("%s: SSRC %x -> %x", c.name, first.ssrc, next.ssrc)
		}
		if next.seq != first.seq+1 {
			t.Fatalf("%s: sequence %d -> %d, want consecutive", c.name, first.seq, next.seq)
		}
		got := int64(next.timestamp - first.timestamp)
		want := ticks(next.at.Sub(first.at))
		if got < ticks(c.pause) || got-want > ticks(3*time.Millisecond) || want-got > ticks(3*time.Millisecond) {
			t.Fatalf("%s: RTP advanced %d ticks over %s, want about %d", c.name, got, next.at.Sub(first.at), want)
		}
		first = next
	}
}
