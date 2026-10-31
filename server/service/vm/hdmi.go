package vm

import (
	"context"
	"sync"
	"time"

	"NanoKVM-Server/common"
	"NanoKVM-Server/internal/startpriority"
	"NanoKVM-Server/internal/videogate"
	"NanoKVM-Server/proto"
	hdmistate "NanoKVM-Server/service/vm/hdmi_state"
	"NanoKVM-Server/utils"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

const hdmiCaptureWarmupDuration = time.Second

// Without viewers the VPSS outputs are parked after this delay, so a page
// reload does not park and resume them.
var hdmiCaptureParkDelay = 20 * time.Second

// Disabled pools can remain busy until the last VPSS job returns its frame.
var hdmiCaptureReclaimDelay = time.Second

var (
	hdmiMutex           sync.Mutex
	hdmiCaptureReadSlot = make(chan struct{}, 1)
	hdmiIdleTimer       *time.Timer
	hdmiIdleGeneration  uint64
	hdmiDemand          = hdmistate.New()
	hdmiStoppedForIdle  bool
	hdmiParkTimer       *time.Timer
	hdmiCaptureParked   bool

	setHDMI                = func(enabled bool) { common.GetKvmVision().SetHDMI(enabled) }
	isHdmiDisabled         = utils.IsHdmiDisabled
	getHdmiIdleTimeout     = utils.GetHDMIIdleTimeout
	persistHdmiIdleTimeout = utils.PersistHDMIIdleTimeout
	getHdmiSignal          = func() bool { return common.GetKvmVision().HasHDMISignal() }
	setCaptureParked       = func(parked bool) bool { return common.GetKvmVision().SetCaptureParked(parked) == 0 }
	captureReclaimPending  = func() bool { return common.GetKvmVision().CaptureReclaimPending() }
)

func (s *Service) ResetHdmi(c *gin.Context) {
	var rsp proto.Response

	// Any user may restart capture, but a reset must never override an
	// administrator's decision to keep HDMI capture disabled.
	if isHdmiDisabled() {
		rsp.ErrRsp(c, -2, "HDMI capture is disabled")
		return
	}
	DisableHdmiCapture()
	time.Sleep(1 * time.Second)
	if !isHdmiDisabled() {
		EnableHdmiCapture()
	}

	rsp.OkRsp(c)
	log.Debug("reset hdmi")
}

func (s *Service) EnableHdmi(c *gin.Context) {
	var rsp proto.Response

	utils.PersistHDMIEnabled()
	EnableHdmiCapture()

	rsp.OkRsp(c)
	log.Debug("enable hdmi")
}

func (s *Service) DisableHdmi(c *gin.Context) {
	var rsp proto.Response

	utils.PersistHDMIDisabled()
	DisableHdmiCapture()

	rsp.OkRsp(c)
	log.Debug("disable hdmi")
}

func (s *Service) GetHdmiState(c *gin.Context) {
	var rsp proto.Response
	rsp.OkRspWithData(c, hdmiState())
	log.Debug("get hdmi state")
}

// HdmiState is GET /api/vm/hdmi for other services (MCP's host_status).
func HdmiState() *proto.GetGetHdmiStateRsp { return hdmiState() }

func hdmiState() *proto.GetGetHdmiStateRsp {
	enabled := !isHdmiDisabled()
	hdmiMutex.Lock()
	viewerCount := hdmiDemand.ViewerCount()
	hdmiMutex.Unlock()
	return &proto.GetGetHdmiStateRsp{
		Enabled:     enabled,
		ViewerCount: viewerCount,
		Signal:      enabled && getHdmiSignal(),
		IdleTimeout: getHdmiIdleTimeout(),
	}
}

func EnableHdmiCapture() {
	hdmiMutex.Lock()
	defer hdmiMutex.Unlock()

	setHDMI(true)
	hdmiDemand.MarkWarming(time.Now().Add(hdmiCaptureWarmupDuration))
	hdmiStoppedForIdle = false
	hdmiIdleGeneration++
	stopHdmiIdleTimerLocked()
	stopHdmiParkTimerLocked()
	scheduleHdmiParkTimerLocked()
	scheduleHdmiIdleTimerLocked()
}

func DisableHdmiCapture() {
	hdmiMutex.Lock()
	defer hdmiMutex.Unlock()

	setHDMI(false)
	hdmiDemand.ClearReadyAt()
	hdmiStoppedForIdle = false
	hdmiIdleGeneration++
	stopHdmiIdleTimerLocked()
	stopHdmiParkTimerLocked()
}

func (s *Service) SetHdmiIdleTimeout(c *gin.Context) {
	var req proto.SetHdmiIdleTimeoutReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}

	SetHdmiIdleTimeout(req.Minutes)
	rsp.OkRsp(c)
}

func SetHdmiIdleTimeout(minutes int) {
	if minutes < 0 || minutes > utils.MaxHDMIIdleTimeoutMinutes {
		return
	}

	persistHdmiIdleTimeout(minutes)

	hdmiMutex.Lock()
	defer hdmiMutex.Unlock()

	hdmiIdleGeneration++
	stopHdmiIdleTimerLocked()
	stopHdmiParkTimerLocked()
	if hdmiStoppedForIdle && !isHdmiDisabled() {
		resumeHdmiCaptureLocked()
		hdmiStoppedForIdle = false
	}
	if !hdmiDemand.HasDemand() && !hdmiStoppedForIdle {
		scheduleHdmiIdleTimerLocked()
	}
	scheduleHdmiParkTimerLocked()
}

func SetHdmiViewerCount(count int) {
	SetHdmiViewerCountForSource("legacy", count)
}

// SetHdmiViewerCountForSource is retained for callers that cannot provide a
// source revision. Streamers should use UpdateHdmiViewerSnapshot instead.
func SetHdmiViewerCountForSource(source string, count int) {
	withViewerGate(count, func() {
		version := hdmiDemand.NextVersion(source)
		updateHdmiViewerSnapshotLocked(source, count, version)
	})
}

// UpdateHdmiViewerSnapshot applies a source's authoritative client count.
// Revisions let streamers report after releasing their own client-map locks
// without allowing an older count to overwrite a newer one.
func UpdateHdmiViewerSnapshot(source string, count int, version uint64) {
	withViewerGate(count, func() {
		updateHdmiViewerSnapshotLocked(source, count, version)
	})
}

var (
	// viewerGateMutex orders the viewer updates with the gate they hold.
	viewerGateMutex sync.Mutex
	viewerGate      func()
	holdVideoGate   = func() func() { release, _ := videogate.Hold(nil); return release }
)

// withViewerGate runs a viewer update under hdmiMutex. A viewer can wake a
// parked capture, which restores its VPSS pools: while there are viewers the
// video gate is held, taken before the update and so before the wake, with
// the background OCR already ended; it is released when none are left.
func withViewerGate(count int, update func()) {
	viewerGateMutex.Lock()
	defer viewerGateMutex.Unlock()
	if count > 0 && viewerGate == nil {
		// Outside hdmiMutex: the OCR being cancelled may wait for it.
		viewerGate = holdVideoGate()
	}
	hdmiMutex.Lock()
	update()
	viewers := hdmiDemand.ViewerCount()
	hdmiMutex.Unlock()
	if viewers == 0 && viewerGate != nil {
		viewerGate()
		viewerGate = nil
	}
}

func updateHdmiViewerSnapshotLocked(source string, count int, version uint64) {
	if count < 0 {
		count = 0
	}

	if !hdmiDemand.UpdateViewer(source, count, version) {
		return
	}
	if count > 0 {
		startpriority.ViewerConnected()
	}
	reconcileHdmiCaptureDemandLocked()
}

// AcquireHdmiCaptureLease prevents idle shutdown while a short-lived capture
// consumer, such as an MCP screenshot, is active. The returned release is
// safe to call more than once.
func AcquireHdmiCaptureLease() func() {
	release, _ := acquireHdmiCaptureLease()
	return release
}

// AcquireHdmiCaptureLeaseForRead keeps capture active and waits outside
// hdmiMutex until a capture resumed from idle has had time to produce a new
// frame. The returned claim function is consumed only by the first successful
// reader, so concurrent readers do not all discard a frame.
func AcquireHdmiCaptureLeaseForRead(ctx context.Context) (func(), func() bool, error) {
	release, readyAt := acquireHdmiCaptureLease()
	if wait := time.Until(readyAt); wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			release()
			return nil, nil, ctx.Err()
		case <-timer.C:
		}
	}
	select {
	case hdmiCaptureReadSlot <- struct{}{}:
	case <-ctx.Done():
		release()
		return nil, nil, ctx.Err()
	}

	var releaseReadOnce sync.Once
	releaseRead := func() {
		releaseReadOnce.Do(func() {
			<-hdmiCaptureReadSlot
			release()
		})
	}

	hdmiMutex.Lock()
	needsFreshFrame := hdmiDemand.NeedsFreshFrame()
	hdmiMutex.Unlock()
	if !needsFreshFrame {
		return releaseRead, nil, nil
	}
	claimFresh := func() bool {
		hdmiMutex.Lock()
		claimed := hdmiDemand.ClaimFreshFrame()
		hdmiMutex.Unlock()
		return claimed
	}
	return releaseRead, claimFresh, nil
}

func acquireHdmiCaptureLease() (func(), time.Time) {
	hdmiMutex.Lock()
	hdmiDemand.AcquireLease()
	reconcileHdmiCaptureDemandLocked()
	readyAt := hdmiDemand.ReadyAt()
	hdmiMutex.Unlock()

	var once sync.Once
	release := func() {
		once.Do(func() {
			hdmiMutex.Lock()
			defer hdmiMutex.Unlock()

			if !hdmiDemand.ReleaseLease() {
				return
			}
			reconcileHdmiCaptureDemandLocked()
		})
	}
	return release, readyAt
}

func reconcileHdmiCaptureDemandLocked() {
	hdmiIdleGeneration++
	stopHdmiIdleTimerLocked()
	stopHdmiParkTimerLocked()

	if hdmiHasCaptureDemandLocked() {
		resumeParkedCaptureLocked()
		if hdmiStoppedForIdle && !isHdmiDisabled() {
			resumeHdmiCaptureLocked()
		}
		hdmiStoppedForIdle = false
		return
	}

	scheduleHdmiParkTimerLocked()
	scheduleHdmiIdleTimerLocked()
}

func stopHdmiParkTimerLocked() {
	if hdmiParkTimer != nil {
		hdmiParkTimer.Stop()
		hdmiParkTimer = nil
	}
}

// Parking disables unused VPSS outputs and releases their pools. Retry while
// parked because a late VPSS job may keep a pool busy after disabling it. The
// HDMI idle timeout below independently stops the input.
func scheduleHdmiParkTimerLocked() {
	if hdmiHasCaptureDemandLocked() || isHdmiDisabled() || hdmiStoppedForIdle {
		return
	}

	delay := hdmiCaptureParkDelay
	if hdmiCaptureParked {
		if !captureReclaimPending() {
			return
		}
		delay = hdmiCaptureReclaimDelay
	}
	generation := hdmiIdleGeneration
	hdmiParkTimer = time.AfterFunc(delay, func() {
		hdmiMutex.Lock()
		defer hdmiMutex.Unlock()

		if generation != hdmiIdleGeneration || hdmiHasCaptureDemandLocked() || isHdmiDisabled() || hdmiStoppedForIdle {
			return
		}
		hdmiParkTimer = nil
		if !setCaptureParked(true) {
			if hdmiCaptureParked {
				scheduleHdmiParkTimerLocked()
			} else {
				log.Info("video capture not parked; it keeps converting frames without viewers")
			}
			return
		}
		if !hdmiCaptureParked {
			hdmiCaptureParked = true
			log.Debug("parked video capture without viewers")
		}
		scheduleHdmiParkTimerLocked()
	})
}

// A frame read resumes parked outputs by itself; resuming here first keeps
// the first read of a new viewer from paying for it.
func resumeParkedCaptureLocked() {
	if !hdmiCaptureParked {
		return
	}
	hdmiCaptureParked = false
	if !setCaptureParked(false) {
		log.Warn("failed to resume parked video capture; the next read retries")
	}
}

func hdmiHasCaptureDemandLocked() bool {
	return hdmiDemand.HasDemand()
}

func stopHdmiIdleTimerLocked() {
	if hdmiIdleTimer != nil {
		hdmiIdleTimer.Stop()
		hdmiIdleTimer = nil
	}
}

func scheduleHdmiIdleTimerLocked() {
	if hdmiHasCaptureDemandLocked() || hdmiStoppedForIdle || isHdmiDisabled() {
		return
	}

	minutes := getHdmiIdleTimeout()
	if minutes == 0 {
		return
	}

	generation := hdmiIdleGeneration
	hdmiIdleTimer = time.AfterFunc(time.Duration(minutes)*time.Minute, func() {
		hdmiMutex.Lock()
		defer hdmiMutex.Unlock()

		if generation != hdmiIdleGeneration || hdmiHasCaptureDemandLocked() || isHdmiDisabled() {
			return
		}

		hdmiIdleTimer = nil
		setHDMI(false)
		hdmiDemand.ClearReadyAt()
		hdmiStoppedForIdle = true
		log.Debugf("disabled hdmi capture after %d minutes without viewers", minutes)
	})
}

func resumeHdmiCaptureLocked() {
	setHDMI(true)
	hdmiDemand.MarkWarming(time.Now().Add(hdmiCaptureWarmupDuration))
	log.Debug("resumed hdmi capture after viewer connected")
}
