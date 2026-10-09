package direct

import (
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"
)

// A drop sequence begins when the queue discards deltas and starts waiting for
// a keyframe, and ends when a keyframe is accepted. The reasons tell apart
// what the stalls of a stream are caused by:
//
//	queue-frames       more frames were pending than the queue depth allows:
//	                   the browser or the network does not drain the stream at
//	                   the capture rate (or ACKs are held back)
//	queue-bytes        the pending bytes exceeded the limit; large frames
//	discontinuity      no client could accept a frame, so the shared source
//	                   advanced past this client; every ACK credit was in flight
//	oversized-delta    one frame alone exceeded the byte limit
//	oversized-keyframe the recovery IDR itself did not fit
//	resync             the browser decoder asked for it (decode error/overload)
//	join               a new viewer waiting for its first keyframe
const (
	dropQueueFrames       = "queue-frames"
	dropQueueBytes        = "queue-bytes"
	dropDiscontinuity     = "discontinuity"
	dropOversizedDelta    = "oversized-delta"
	dropOversizedKeyframe = "oversized-keyframe"
	dropResync            = "resync"
	dropJoin              = "join"

	dropLogInterval = time.Second
)

// dropDiagnostics is owned by a frameQueue and guarded by its mutex.
type dropDiagnostics struct {
	overflows           uint64 // delta refused because the queue was full
	overflowFrames      uint64 // frames lost to those overflows, queued and incoming
	overflowBytes       uint64
	discontinuities     uint64 // sequences begun because no client could accept a frame
	discontinuityFrames uint64
	oversizedDeltas     uint64
	oversizedKeyframes  uint64
	resyncs             uint64 // sequences begun by a browser resync request
	deltasRefused       uint64 // deltas refused while already waiting for a keyframe
	keyframeRequests    uint64
	recoveries          uint64
	maxRecovery         time.Duration

	waitSince     time.Time
	waitReason    string
	sequenceDrops uint64 // deltas refused in the current sequence
	lastLog       time.Time
}

// dropEvent is what a drop site tells the diagnostics.
type dropEvent struct {
	reason       string
	queuedFrames int
	queuedBytes  int
	frameBytes   int
}

func (d *dropDiagnostics) begin(reason string, now time.Time) {
	d.waitSince = now
	d.waitReason = reason
	d.sequenceDrops = 0
}

// note returns the Debug line for a drop site, at most once per dropLogInterval.
// The counters keep counting while lines are suppressed.
func (d *dropDiagnostics) note(now time.Time, event dropEvent, q *frameQueue) string {
	if !log.IsLevelEnabled(log.DebugLevel) {
		return ""
	}
	if !d.lastLog.IsZero() && now.Sub(d.lastLog) < dropLogInterval {
		return ""
	}
	d.lastLog = now
	return fmt.Sprintf("direct stream drop: reason=%s frame_bytes=%d queued=%d/%d frames %d/%d bytes in_flight=%d/%d "+
		"overflows=%d discontinuities=%d oversized=%d/%d refused_deltas=%d keyframe_requests=%d",
		event.reason, event.frameBytes, event.queuedFrames, q.maxFrames, event.queuedBytes, q.maxBytes,
		len(q.inFlight), q.window, d.overflows, d.discontinuities, d.oversizedDeltas, d.oversizedKeyframes,
		d.deltasRefused, d.keyframeRequests)
}

// recovered closes the sequence; the line is not rate limited because a long
// wait is exactly what has to be visible.
func (d *dropDiagnostics) recovered(now time.Time) string {
	waited := max(now.Sub(d.waitSince), 0)
	d.recoveries++
	d.maxRecovery = max(d.maxRecovery, waited)
	if !log.IsLevelEnabled(log.DebugLevel) {
		return ""
	}
	return fmt.Sprintf("direct stream recovered: reason=%s waited=%s refused_deltas=%d keyframe_requests=%d",
		d.waitReason, waited.Round(time.Millisecond), d.sequenceDrops, d.keyframeRequests)
}

// summary is logged once when the client disconnects. It is empty for a
// connection that never lost frames.
func (d *dropDiagnostics) summary() string {
	if d.overflows+d.discontinuities+d.oversizedDeltas+d.oversizedKeyframes+d.resyncs == 0 {
		return ""
	}
	return fmt.Sprintf("overflows=%d overflow_frames=%d overflow_bytes=%d discontinuities=%d "+
		"discontinuity_frames=%d oversized_deltas=%d oversized_keyframes=%d resyncs=%d refused_deltas=%d "+
		"keyframe_requests=%d recoveries=%d max_recovery=%s",
		d.overflows, d.overflowFrames, d.overflowBytes, d.discontinuities, d.discontinuityFrames,
		d.oversizedDeltas, d.oversizedKeyframes, d.resyncs, d.deltasRefused, d.keyframeRequests,
		d.recoveries, d.maxRecovery.Round(time.Millisecond))
}
