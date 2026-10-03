package webrtc

import (
	"NanoKVM-Server/service/stream"
	"os"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
)

const peerWriterDiagnosticsEnv = "NANOKVM_WEBRTC_DIAGNOSTICS"

type peerWriterDropReason uint8

const (
	peerWriterDropRepairing peerWriterDropReason = iota + 1
	peerWriterDropClosed
)

type peerWriterDiagnostics struct {
	id atomic.Uint64

	offers   atomic.Uint64
	accepted atomic.Uint64
	written  atomic.Uint64

	repairingDropped atomic.Uint64
	closedDropped    atomic.Uint64
	overflow         atomic.Uint64
	drainEvents      atomic.Uint64
	drainedFrames    atomic.Uint64
	budgetErrors     atomic.Uint64

	sentBytes          atomic.Uint64
	droppedBytes       atomic.Uint64
	maxFrameSize       atomic.Uint64
	queueHighWater     atomic.Uint64
	queueByteHighWater atomic.Uint64
	queueWaitCount     atomic.Uint64
	queueWaitNs        atomic.Uint64
	maxQueueWaitNs     atomic.Uint64

	writeCalls      atomic.Uint64
	writeDurationNs atomic.Uint64
	maxWriteNs      atomic.Uint64

	writtenKeyframes atomic.Uint64
	writtenDeltas    atomic.Uint64
	drainedKeyframes atomic.Uint64
	drainedDeltas    atomic.Uint64

	offeredKeyframes atomic.Uint64
	offeredDeltas    atomic.Uint64
	offeredErrors    atomic.Uint64
	logged           atomic.Bool
}

var peerWriterDiagnosticsID atomic.Uint64

func newPeerWriterDiagnostics() *peerWriterDiagnostics {
	if os.Getenv(peerWriterDiagnosticsEnv) != "1" {
		return nil
	}
	d := &peerWriterDiagnostics{}
	d.id.Store(peerWriterDiagnosticsID.Add(1))
	return d
}

func (d *peerWriterDiagnostics) recordOffer(frame stream.VideoFrame) {
	d.offers.Add(1)
	switch {
	case frame.IsKeyframe():
		d.offeredKeyframes.Add(1)
	case frame.Result < 0:
		d.offeredErrors.Add(1)
	default:
		d.offeredDeltas.Add(1)
	}
	d.observeMax(&d.maxFrameSize, uint64(len(frame.Data)))
}

func (d *peerWriterDiagnostics) recordAccepted(queueLen, queueBytes int) {
	d.accepted.Add(1)
	d.observeMax(&d.queueHighWater, uint64(queueLen))
	d.observeMax(&d.queueByteHighWater, uint64(queueBytes))
}

func (d *peerWriterDiagnostics) recordQueueWait(elapsed time.Duration) {
	d.queueWaitCount.Add(1)
	ns := uint64(elapsed)
	d.queueWaitNs.Add(ns)
	d.observeMax(&d.maxQueueWaitNs, ns)
}

func (d *peerWriterDiagnostics) recordWrite(frame stream.VideoFrame, elapsed time.Duration, err error) {
	d.writeCalls.Add(1)
	ns := uint64(elapsed)
	d.writeDurationNs.Add(ns)
	d.observeMax(&d.maxWriteNs, ns)
	if err == nil {
		d.written.Add(1)
		d.sentBytes.Add(uint64(len(frame.Data)))
		if frame.IsKeyframe() {
			d.writtenKeyframes.Add(1)
		} else if frame.Result >= 0 {
			d.writtenDeltas.Add(1)
		}
		return
	}
	d.droppedBytes.Add(uint64(len(frame.Data)))
}

func (d *peerWriterDiagnostics) recordDrop(frame stream.VideoFrame, reason peerWriterDropReason) {
	d.droppedBytes.Add(uint64(len(frame.Data)))
	switch reason {
	case peerWriterDropRepairing:
		d.repairingDropped.Add(1)
	case peerWriterDropClosed:
		d.closedDropped.Add(1)
	}
}

func (d *peerWriterDiagnostics) recordOverflow() {
	d.overflow.Add(1)
}

func (d *peerWriterDiagnostics) recordDrainStart() {
	d.drainEvents.Add(1)
}

func (d *peerWriterDiagnostics) recordDrained(frame stream.VideoFrame) {
	d.drainedFrames.Add(1)
	d.droppedBytes.Add(uint64(len(frame.Data)))
	if frame.IsKeyframe() {
		d.drainedKeyframes.Add(1)
	} else if frame.Result >= 0 {
		d.drainedDeltas.Add(1)
	}
}

func (d *peerWriterDiagnostics) recordBudgetError() {
	d.budgetErrors.Add(1)
}

func (d *peerWriterDiagnostics) observeMax(target *atomic.Uint64, value uint64) {
	for {
		old := target.Load()
		if value <= old || target.CompareAndSwap(old, value) {
			return
		}
	}
}

func (d *peerWriterDiagnostics) logClose() {
	if !d.logged.CompareAndSwap(false, true) {
		return
	}
	queueWaitCount := d.queueWaitCount.Load()
	queueWaitNs := d.queueWaitNs.Load()
	queueWaitAvgNs := uint64(0)
	if queueWaitCount != 0 {
		queueWaitAvgNs = queueWaitNs / queueWaitCount
	}
	log.Infof(
		"webrtc peer writer diagnostics id=%d offers=%d accepted=%d written=%d repairing_dropped=%d closed_dropped=%d overflow=%d drain_events=%d drained_frames=%d err_video_budget=%d sent_bytes=%d dropped_bytes=%d max_frame_bytes=%d queue_high_water=%d queue_bytes_high_water=%d queue_wait_count=%d queue_wait_ns_total=%d queue_wait_ns_avg=%d queue_wait_ns_max=%d write_calls=%d write_ns_total=%d write_ns_max=%d written_keyframes=%d written_deltas=%d drained_keyframes=%d drained_deltas=%d offered_keyframes=%d offered_deltas=%d offered_errors=%d",
		d.id.Load(),
		d.offers.Load(),
		d.accepted.Load(),
		d.written.Load(),
		d.repairingDropped.Load(),
		d.closedDropped.Load(),
		d.overflow.Load(),
		d.drainEvents.Load(),
		d.drainedFrames.Load(),
		d.budgetErrors.Load(),
		d.sentBytes.Load(),
		d.droppedBytes.Load(),
		d.maxFrameSize.Load(),
		d.queueHighWater.Load(),
		d.queueByteHighWater.Load(),
		queueWaitCount,
		queueWaitNs,
		queueWaitAvgNs,
		d.maxQueueWaitNs.Load(),
		d.writeCalls.Load(),
		d.writeDurationNs.Load(),
		d.maxWriteNs.Load(),
		d.writtenKeyframes.Load(),
		d.writtenDeltas.Load(),
		d.drainedKeyframes.Load(),
		d.drainedDeltas.Load(),
		d.offeredKeyframes.Load(),
		d.offeredDeltas.Load(),
		d.offeredErrors.Load(),
	)
}
