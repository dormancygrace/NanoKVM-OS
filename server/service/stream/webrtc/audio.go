package webrtc

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"time"

	"NanoKVM-Server/service/stream/audio"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	log "github.com/sirupsen/logrus"
)

// mediaStreamID groups the video and USB audio tracks of one peer connection.
// Browsers synchronize the playout of tracks in one MediaStream, using the
// RTCP sender reports of both (ConfigureRTCPReports).
const mediaStreamID = "nanokvm"

const opusPayloadType = 111

// maxAudioDelay bounds NANOKVM_WEBRTC_AUDIO_DELAY_MS.
const maxAudioDelay = 500 * time.Millisecond

// audioFrame is the duration of one encoded Opus packet.
const audioFrame = 20 * time.Millisecond

// packetSource is the shared capture as seen by one listener.
type packetSource interface {
	Read(ctx context.Context) (audio.Packet, error)
	Close()
}

// subscribeAudio is replaced in tests.
var subscribeAudio = func() (packetSource, error) {
	sub, err := audio.Subscribe()
	if err != nil {
		return nil, err
	}
	return sub, nil
}

var (
	errAudioNotNegotiated = errors.New("audio was not negotiated on this connection")
	errAudioBusy          = errors.New("too many audio sessions")
)

// The audio of a video peer connection. The track is attached once, when an
// offer first carries an audio m-line that Pion can send on; it then stays
// for the life of the connection (Pion stops a transceiver for good when the
// remote marks it inactive). Listening is separate: audio-start subscribes
// to the shared capture and encoder, audio-stop unsubscribes, so a viewer
// with the speaker off does not keep the Opus encoder running.
//
// The RTP clock of the track must keep pace with real time across pauses
// (RFC 3550 5.1): receivers and RTCP sender reports map it to wall-clock
// time for lip sync. lastSent records when the last packet was written, and
// the next listening period first advances the clock by the muted interval;
// within a period, a pause of the capture (the computer plays nothing)
// advances it by the time between the two packets.
type peerAudio struct {
	mu       sync.Mutex
	track    *webrtc.TrackLocalStaticSample
	cancel   context.CancelFunc
	done     chan struct{}
	lastSent time.Time
}

// audioDelay holds audio back before sending, to line it up with video when
// the browser's lip sync is off by the difference of the two capture paths.
// Sender reports map RTP time to the time of sending, so a constant extra
// encoder latency of the video shows as audio leading by the same amount.
func audioDelay() time.Duration {
	ms, err := strconv.Atoi(os.Getenv("NANOKVM_WEBRTC_AUDIO_DELAY_MS"))
	if err != nil || ms <= 0 {
		return 0
	}
	return min(time.Duration(ms)*time.Millisecond, maxAudioDelay)
}

func opusCapability() webrtc.RTPCodecCapability {
	return webrtc.RTPCodecCapability{
		MimeType:    webrtc.MimeTypeOpus,
		ClockRate:   audio.SampleRate,
		Channels:    audio.Channels,
		SDPFmtpLine: audio.OpusFmtpLine,
	}
}

// attachAudioTrack runs after SetRemoteDescription and before CreateAnswer.
// If the offer carries an audio m-line the connection does not send on yet,
// it attaches the audio track to that transceiver, so the answer sends audio.
func (c *Client) attachAudioTrack() error {
	c.audio.mu.Lock()
	defer c.audio.mu.Unlock()
	if c.audio.track != nil {
		return nil
	}
	var target *webrtc.RTPTransceiver
	for _, t := range c.video.GetTransceivers() {
		if t.Kind() == webrtc.RTPCodecTypeAudio && t.Sender() == nil &&
			t.Direction() != webrtc.RTPTransceiverDirectionInactive {
			target = t
			break
		}
	}
	if target == nil {
		return nil
	}
	track, err := webrtc.NewTrackLocalStaticSample(opusCapability(), "audio", mediaStreamID)
	if err != nil {
		return err
	}
	sender, err := c.video.AddTrack(track)
	if err != nil {
		return err
	}
	if target.Sender() != sender {
		// AddTrack made a new transceiver: this answer cannot carry it.
		_ = c.video.RemoveTrack(sender)
		return errAudioNotNegotiated
	}
	go drainRTCP(sender)
	c.audio.track = track
	return nil
}

func drainRTCP(sender *webrtc.RTPSender) {
	buf := make([]byte, 1500)
	for {
		if _, _, err := sender.Read(buf); err != nil {
			return
		}
	}
}

// startAudio subscribes this connection's audio track to the shared capture.
func (c *Client) startAudio() error {
	c.audio.mu.Lock()
	defer c.audio.mu.Unlock()
	if c.audio.track == nil {
		return errAudioNotNegotiated
	}
	if c.audio.cancel != nil {
		return nil
	}
	if !audio.AcquireSession() {
		return errAudioBusy
	}
	sub, err := subscribeAudio()
	if err != nil {
		audio.ReleaseSession()
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.audio.cancel, c.audio.done = cancel, done
	go c.runAudio(ctx, sub, c.audio.track, done)
	return nil
}

// stopAudio unsubscribes and waits for the sample loop to end.
func (c *Client) stopAudio() {
	c.audio.mu.Lock()
	cancel, done := c.audio.cancel, c.audio.done
	c.audio.cancel, c.audio.done = nil, nil
	c.audio.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

type timedPacket struct {
	due    time.Time
	packet audio.Packet
}

func (c *Client) runAudio(ctx context.Context, sub packetSource, track *webrtc.TrackLocalStaticSample, done chan struct{}) {
	defer close(done)
	defer audio.ReleaseSession()
	defer sub.Close()
	delay := audioDelay()
	packets := make(chan timedPacket, 64)
	readErr := make(chan error, 1)
	go func() {
		defer close(packets)
		for {
			p, err := sub.Read(ctx)
			if err != nil {
				readErr <- err
				return
			}
			select {
			case packets <- timedPacket{due: time.Now().Add(delay), packet: p}:
			case <-ctx.Done():
				readErr <- ctx.Err()
				return
			}
		}
	}()
	var last uint64
	var lastAt time.Time
	haveLast := false
	first := true
	for tp := range packets {
		if wait := time.Until(tp.due); wait > 0 {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
			}
		}
		if ctx.Err() != nil {
			break
		}
		if first {
			// The capture may have restarted since the last period (its index
			// too), so the muted interval comes from the clock, not the index.
			first = false
			c.audio.mu.Lock()
			prev := c.audio.lastSent
			c.audio.mu.Unlock()
			if !prev.IsZero() {
				if err := audio.AdvanceTimeline(track, time.Since(prev)-audioFrame); err != nil {
					log.Debugf("audio timeline: %v", err)
					break
				}
			}
		}
		dropped := uint64(0)
		if haveLast && tp.packet.Index > last+1 {
			dropped = min(tp.packet.Index-last-1, 65535)
		}
		if haveLast {
			if err := audio.AdvanceTimeline(track, audio.PauseGap(tp.packet.At.Sub(lastAt), dropped)); err != nil {
				log.Debugf("audio timeline: %v", err)
				break
			}
		}
		if err := track.WriteSample(media.Sample{Data: tp.packet.Data, Duration: audioFrame,
			PrevDroppedPackets: uint16(dropped)}); err != nil {
			log.Debugf("audio track write: %v", err)
			break
		}
		c.audio.mu.Lock()
		c.audio.lastSent = time.Now()
		c.audio.mu.Unlock()
		last, lastAt, haveLast = tp.packet.Index, tp.packet.At, true
	}
	if ctx.Err() == nil {
		// Capture ended (USB audio disabled or rebound) or the track failed.
		c.audio.mu.Lock()
		if c.audio.done == done {
			c.audio.cancel()
			c.audio.cancel, c.audio.done = nil, nil
		}
		c.audio.mu.Unlock()
		if err := c.WriteMessage("audio-stopped", ""); err != nil {
			log.Debugf("failed to send audio-stopped: %v", err)
		}
	}
	for range packets {
	}
}
