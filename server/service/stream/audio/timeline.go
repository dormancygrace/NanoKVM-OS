package audio

import (
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// FrameDuration is the length of one encoded Opus packet.
const FrameDuration = 20 * time.Millisecond

// The capture delivers nothing while the computer plays no sound, and the
// encoder's packets arrive in bursts; gaps up to this long are such jitter.
// The correction only moves the clock forward, so one late delivery taken for
// a pause would leave the clock ahead for the rest of the session: a missed
// short pause is the smaller error. Recordings re-anchor audio that is late by
// the same amount.
const pauseThreshold = 500 * time.Millisecond

// PauseGap is how far the RTP clock of a track has to jump before a packet
// captured elapsed after the previous one: the RTP clock must keep pace with
// real time (RFC 3550 5.1), receivers and recordings map it to wall-clock
// time. The previous packet's 20 ms and the dropped frames, which
// PrevDroppedPackets covers, are not part of the gap. Shorter gaps are
// capture jitter and must not become timestamp jumps.
func PauseGap(elapsed time.Duration, dropped uint64) time.Duration {
	gap := elapsed - FrameDuration*time.Duration(dropped+1)
	if gap < pauseThreshold {
		return 0
	}
	return gap
}

// AdvanceTimeline moves the RTP clock of track over gap without sending
// anything: for an empty payload Pion's packetizer only skips the samples,
// and no sequence numbers are used, so receivers see no loss. Steps of an
// hour keep each skip far inside the 32-bit timestamp.
func AdvanceTimeline(track *webrtc.TrackLocalStaticSample, gap time.Duration) error {
	for gap > 0 {
		step := min(gap, time.Hour)
		if err := track.WriteSample(media.Sample{Duration: step}); err != nil {
			return err
		}
		gap -= step
	}
	return nil
}
