package direct

import (
	"NanoKVM-Server/service/stream"
	"encoding/binary"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

const (
	frameAckMessage     byte = 2
	streamResyncMessage byte = 3

	// The pending queue holds about queueDelay of video, so a stall of a given
	// length costs the same at any stream rate; the frame count only bounds
	// it. defaultQueueFrames is the floor, which keeps low rates as tolerant
	// as they were with a fixed depth.
	defaultQueueFrames = 8
	maxQueueFrames     = 24
	queueDelay         = 150 * time.Millisecond
	defaultQueueBytes  = 2 * 1024 * 1024
	// A request is a coalesced flag on the capture owner, which applies it at
	// most every 500 ms and the encoder then needs a frame or two to emit the
	// IDR. Repeating more often than that would only provoke a second IDR for
	// a request that is still being served.
	keyframeRequestInterval = time.Second
	// A frame larger than the byte limit suggests that the next IDR will not
	// fit either, so requests are held back for oversizedBackoffMin. That is
	// a guess, not a proof: the scene or the bitrate can change. Each further
	// oversized frame after a window has run out doubles the window up to
	// oversizedBackoffMax, so the retries stay bounded but never stop.
	oversizedBackoffMin = 2 * time.Second
	oversizedBackoffMax = 30 * time.Second
	// Keep in step with the worker (direct.worker.ts): decoderHighWatermark +
	// flowControlWindow must stay below maxPendingDecodes, or a held ACK
	// could let the decoder reach the overload reset by itself.
	maxFlowWindow = 8
	writeWait     = 2 * time.Second
	pingPeriod    = 15 * time.Second
)

type outboundFrame struct {
	key       bool
	timestamp int64
	payload   []byte
	// duration is one frame interval at the stream rate; zero if unknown.
	duration time.Duration
}

// queueDepthFor is the number of pending frames that cover about queueDelay
// at the rate of the given frame interval.
func queueDepthFor(duration time.Duration) int {
	if duration <= 0 {
		return defaultQueueFrames
	}
	frames := int((queueDelay + duration/2) / duration)
	return min(max(frames, defaultQueueFrames), maxQueueFrames)
}

type frameQueue struct {
	mutex sync.Mutex
	wake  chan struct{}

	frames      []*outboundFrame
	queuedBytes int
	inFlight    []int64

	maxFrames int
	maxBytes  int
	window    int
	// timeBased lets the frame interval of offered frames set maxFrames.
	timeBased bool

	flowControlled     bool
	waitingForKeyframe bool
	closed             bool

	// requestKeyframe asks the capture owner for an IDR. It must not block;
	// it is called with the queue mutex held.
	requestKeyframe     func()
	now                 func() time.Time
	lastKeyframeRequest time.Time
	// A keyframe (or a delta) larger than maxBytes suggests that an IDR cannot
	// be queued either, and requesting more of them wastes encoder time.
	// Requests are suppressed for oversizedBackoff from oversizedAt; the window
	// grows while oversized frames keep arriving after it ends and is forgotten
	// by an accepted keyframe. oversizedBackoff == 0 means no oversized frame
	// is being tracked.
	oversizedAt      time.Time
	oversizedBackoff time.Duration

	diag dropDiagnostics
	// note is a Debug line produced under the mutex and logged after it.
	note string
}

func newFrameQueue(maxFrames int, maxBytes int) *frameQueue {
	q := &frameQueue{
		wake:               make(chan struct{}, 1),
		maxFrames:          maxFrames,
		maxBytes:           maxBytes,
		waitingForKeyframe: true,
		requestKeyframe:    stream.RequestKeyframe,
		now:                time.Now,
	}
	q.diag.begin(dropJoin, q.now())
	return q
}

// unlockAndLog releases the mutex before the line reaches the logger, so a
// slow log sink never delays the writer or the ACK handler.
func (q *frameQueue) unlockAndLog() {
	note := q.note
	q.note = ""
	q.mutex.Unlock()
	if note != "" {
		log.Debug(note)
	}
}

// startWaitLocked makes the queue wait for a keyframe. It reports whether this
// began a new drop sequence; frames refused inside one are only counted.
func (q *frameQueue) startWaitLocked(reason string) bool {
	if q.waitingForKeyframe {
		return false
	}
	q.waitingForKeyframe = true
	q.diag.begin(reason, q.now())
	return true
}

func (q *frameQueue) noteLocked(event dropEvent) {
	if note := q.diag.note(q.now(), event, q); note != "" {
		q.note = note
	}
}

// summary describes the drops of this connection, or "" if there were none.
func (q *frameQueue) summary() string {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	return q.diag.summary()
}

// beginOversizedLocked records that a frame exceeded the byte limit and opens
// the window in which no recovery IDR is requested. A frame inside a running
// window changes nothing, so a stream of oversized frames cannot push the
// window out indefinitely or escalate it faster than it elapses. The first
// frame after a window grows it (2 s, 4 s, ... up to 30 s), since the request
// that was allowed meanwhile did not help. After a full oversizedBackoffMax
// without any oversized frame the history is dropped and the window starts at
// the minimum again.
func (q *frameQueue) beginOversizedLocked(now time.Time) {
	end := q.oversizedAt.Add(q.oversizedBackoff)
	switch {
	case q.oversizedBackoff == 0 || now.Sub(end) >= oversizedBackoffMax:
		q.oversizedBackoff = oversizedBackoffMin
	case now.Before(end):
		return
	default:
		q.oversizedBackoff = min(q.oversizedBackoff*2, oversizedBackoffMax)
	}
	q.oversizedAt = now
}

// oversizedSuppressedLocked reports whether a recent oversized frame still
// holds requests back.
func (q *frameQueue) oversizedSuppressedLocked(now time.Time) bool {
	return q.oversizedBackoff != 0 && now.Before(q.oversizedAt.Add(q.oversizedBackoff))
}

func (q *frameQueue) resetOversizedLocked() {
	q.oversizedAt = time.Time{}
	q.oversizedBackoff = 0
}

// requestKeyframeLocked starts or repeats the request for a recovery IDR.
// Callers invoke it whenever the queue is (still) waiting for a keyframe;
// the interval turns that into one request per drop sequence plus a repeat
// while the stream stays stalled. It does nothing while an oversized frame
// holds requests back (see beginOversizedLocked).
func (q *frameQueue) requestKeyframeLocked() {
	now := q.now()
	if q.oversizedSuppressedLocked(now) {
		return
	}
	if !q.lastKeyframeRequest.IsZero() && now.Sub(q.lastKeyframeRequest) < keyframeRequestInterval {
		return
	}
	q.lastKeyframeRequest = now
	q.diag.keyframeRequests++
	q.requestKeyframe()
}

func (q *frameQueue) enableFlowControl(window int) {
	if window < 1 {
		window = 1
	}
	if window > maxFlowWindow {
		window = maxFlowWindow
	}

	q.mutex.Lock()
	q.clearFramesLocked()
	q.inFlight = q.inFlight[:0]
	q.window = window
	q.flowControlled = true
	q.waitingForKeyframe = true
	q.diag.begin(dropJoin, q.now())
	q.resetOversizedLocked()
	q.mutex.Unlock()
}

func (q *frameQueue) canAdvanceStream() bool {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	if q.closed {
		return false
	}
	if !q.flowControlled {
		return true
	}

	// ACK credits bound socket writes, not the pending capture queue. Brief
	// network jitter must not discard a valid inter-frame prediction chain.
	return q.waitingForKeyframe || (len(q.frames) < q.maxFrames && q.queuedBytes < q.maxBytes)
}

func (q *frameQueue) captureState() (active bool, flowControlled bool, canAdvance bool) {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	if q.closed {
		return false, q.flowControlled, false
	}
	if !q.flowControlled {
		return true, false, true
	}
	return true, true, q.waitingForKeyframe || (len(q.frames) < q.maxFrames && q.queuedBytes < q.maxBytes)
}

func (q *frameQueue) offer(frame *outboundFrame) bool {
	q.mutex.Lock()
	defer q.unlockAndLog()

	if q.closed {
		return false
	}
	if q.timeBased && frame.duration > 0 {
		q.maxFrames = queueDepthFor(frame.duration)
	}

	if frame.key {
		queuedFrames, queuedBytes := len(q.frames), q.queuedBytes
		q.clearFramesLocked()
		if len(frame.payload) > q.maxBytes {
			q.startWaitLocked(dropOversizedKeyframe)
			q.beginOversizedLocked(q.now())
			q.diag.oversizedKeyframes++
			q.noteLocked(dropEvent{dropOversizedKeyframe, queuedFrames, queuedBytes, len(frame.payload)})
			return false
		}
		if q.waitingForKeyframe {
			q.note = q.diag.recovered(q.now())
		}
		q.waitingForKeyframe = false
		q.resetOversizedLocked()
		q.pushLocked(frame)
		q.signalLocked()
		return true
	}

	oversized := len(frame.payload) > q.maxBytes
	if q.waitingForKeyframe {
		// The browser decoder is still configured and cannot tell that frames
		// stopped, so it never sends a resync. Keep asking for the IDR here.
		q.diag.deltasRefused++
		q.diag.sequenceDrops++
		if oversized {
			q.diag.oversizedDeltas++
		}
		q.dropDeltaLocked(oversized)
		return false
	}

	if len(q.frames) >= q.maxFrames || q.queuedBytes+len(frame.payload) > q.maxBytes {
		event := dropEvent{dropQueueFrames, len(q.frames), q.queuedBytes, len(frame.payload)}
		if len(q.frames) < q.maxFrames {
			event.reason = dropQueueBytes
		}
		if oversized {
			event.reason = dropOversizedDelta
		}
		q.diag.overflows++
		q.diag.overflowFrames += uint64(len(q.frames)) + 1
		q.diag.overflowBytes += uint64(q.queuedBytes + len(frame.payload))
		if oversized {
			q.diag.oversizedDeltas++
		}
		q.clearFramesLocked()
		q.startWaitLocked(event.reason)
		q.dropDeltaLocked(oversized)
		q.noteLocked(event)
		return false
	}

	q.pushLocked(frame)
	q.signalLocked()
	return true
}

// dropDeltaLocked asks for a recovery IDR after a delta was refused. A delta
// that alone exceeds the byte limit starts the suppression window
// instead: the IDR would probably not fit either. When such a frame arrives
// after the previous window ran out, the request that window held back is made
// first, so a stream of oversized deltas still gets a bounded retry.
func (q *frameQueue) dropDeltaLocked(oversized bool) {
	if oversized {
		now := q.now()
		if q.oversizedBackoff != 0 && !q.oversizedSuppressedLocked(now) {
			q.requestKeyframeLocked()
		}
		q.beginOversizedLocked(now)
		return
	}
	q.requestKeyframeLocked()
}

func (q *frameQueue) popForWrite() *outboundFrame {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	if q.closed || len(q.frames) == 0 || (q.flowControlled && len(q.inFlight) >= q.window) {
		return nil
	}

	frame := q.frames[0]
	q.frames[0] = nil
	q.frames = q.frames[1:]
	q.queuedBytes -= len(frame.payload)
	if q.flowControlled {
		q.inFlight = append(q.inFlight, frame.timestamp)
	}

	return frame
}

func (q *frameQueue) acknowledge(timestamp int64) {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	acknowledged := 0
	for acknowledged < len(q.inFlight) && q.inFlight[acknowledged] <= timestamp {
		acknowledged++
	}
	if acknowledged == 0 {
		return
	}

	clear(q.inFlight[:acknowledged])
	q.inFlight = q.inFlight[acknowledged:]
	q.signalLocked() // The writer may be sleeping with queued frames but no ACK credits.
}

func (q *frameQueue) requestResync() {
	q.mutex.Lock()
	defer q.unlockAndLog()
	queuedFrames, queuedBytes := len(q.frames), q.queuedBytes
	q.clearFramesLocked()
	q.inFlight = q.inFlight[:0]
	if q.startWaitLocked(dropResync) {
		q.diag.resyncs++
		q.noteLocked(dropEvent{dropResync, queuedFrames, queuedBytes, 0})
	}
	q.resetOversizedLocked()
	// The caller forwards the browser request itself; do not repeat it for
	// the deltas that are refused until that IDR arrives.
	q.lastKeyframeRequest = q.now()
}

func (q *frameQueue) markDiscontinuity() {
	q.mutex.Lock()
	defer q.unlockAndLog()
	queuedFrames, queuedBytes := len(q.frames), q.queuedBytes
	q.clearFramesLocked()
	if q.startWaitLocked(dropDiscontinuity) {
		q.diag.discontinuities++
		q.diag.discontinuityFrames += uint64(queuedFrames)
		q.noteLocked(dropEvent{dropDiscontinuity, queuedFrames, queuedBytes, 0})
	}
	// The shared source skipped a frame for this client, so only an IDR
	// can resume it; do not wait for the natural GOP boundary.
	q.requestKeyframeLocked()
}

func (q *frameQueue) close() {
	q.mutex.Lock()
	q.closed = true
	q.clearFramesLocked()
	q.inFlight = q.inFlight[:0]
	q.mutex.Unlock()
}

func (q *frameQueue) pushLocked(frame *outboundFrame) {
	q.frames = append(q.frames, frame)
	q.queuedBytes += len(frame.payload)
}

func (q *frameQueue) clearFramesLocked() {
	clear(q.frames)
	q.frames = q.frames[:0]
	q.queuedBytes = 0
}

func (q *frameQueue) signalLocked() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

type client struct {
	conn  *websocket.Conn
	queue *frameQueue

	done       chan struct{}
	writerDone chan struct{}
	closeOnce  sync.Once
}

func newClient(conn *websocket.Conn) *client {
	queue := newFrameQueue(defaultQueueFrames, defaultQueueBytes)
	queue.timeBased = true
	return &client{
		conn:       conn,
		queue:      queue,
		done:       make(chan struct{}),
		writerDone: make(chan struct{}),
	}
}

func (c *client) start() {
	go c.writeLoop()
}

func (c *client) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		c.queue.close()
		_ = c.conn.Close()
	})
}

func (c *client) wait() {
	<-c.writerDone
}

func (c *client) captureState() (active bool, flowControlled bool, canAdvance bool) {
	return c.queue.captureState()
}

func hasCaptureDemand(clients []*client) bool {
	for _, client := range clients {
		active, flowControlled, canAdvance := client.captureState()
		if !active {
			continue
		}
		if !flowControlled || canAdvance {
			return true
		}
	}

	return false
}

func (c *client) offer(frame *outboundFrame) {
	c.queue.offer(frame)
}

func (c *client) markDiscontinuity() {
	c.queue.markDiscontinuity()
}

func (c *client) handleControl(messageType int, data []byte) {
	if messageType != websocket.BinaryMessage || len(data) == 0 {
		return
	}

	switch data[0] {
	case frameAckMessage:
		if len(data) == 9 {
			timestamp := int64(binary.LittleEndian.Uint64(data[1:]))
			c.queue.acknowledge(timestamp)
		}
	case streamResyncMessage:
		if len(data) == 1 {
			c.queue.requestResync()
			stream.RequestKeyframe()
		}
	}
}

func newOutboundFrame(isKeyFrame bool, timestamp int64, storage []byte, data []byte) *outboundFrame {
	reuseStorage := len(storage) == 9+len(data)
	if reuseStorage && len(data) != 0 {
		reuseStorage = &storage[9] == &data[0]
	}

	payload := storage
	if !reuseStorage {
		payload = make([]byte, 9+len(data))
		copy(payload[9:], data)
	}
	payload[0] = 0
	if isKeyFrame {
		payload[0] = 1
	}
	binary.LittleEndian.PutUint64(payload[1:9], uint64(timestamp))

	return &outboundFrame{
		key:       isKeyFrame,
		timestamp: timestamp,
		payload:   payload,
	}
}

func (c *client) writeLoop() {
	defer close(c.writerDone)

	pingTicker := time.NewTicker(pingPeriod)
	defer pingTicker.Stop()

	for {
		select {
		case <-pingTicker.C:
			deadline := time.Now().Add(writeWait)
			if err := c.conn.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
				c.close()
				return
			}
		default:
		}

		if frame := c.queue.popForWrite(); frame != nil {
			if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				c.close()
				return
			}
			if err := c.conn.WriteMessage(websocket.BinaryMessage, frame.payload); err != nil {
				log.Debugf("failed to write h264 frame to %s: %s", c.conn.RemoteAddr(), err)
				c.close()
				return
			}
			continue
		}

		select {
		case <-c.done:
			return
		case <-c.queue.wake:
		case <-pingTicker.C:
			deadline := time.Now().Add(writeWait)
			if err := c.conn.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
				c.close()
				return
			}
		}
	}
}
