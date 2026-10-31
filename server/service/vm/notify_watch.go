package vm

import (
	"context"
	"sync"
	"time"

	"NanoKVM-Server/pkg/bmc"
	"NanoKVM-Server/service/notify"
)

const (
	notifyWatchInterval = time.Second
	// The power LED of a sleeping host blinks: only a state that holds this
	// long is a change.
	notifyPowerHold = 5 * time.Second
	// A mode switch drops the HDMI signal for a moment.
	notifyHDMIHold = 10 * time.Second
	// After capture starts, the signal settles before it is believed.
	notifyHDMISettle = 10 * time.Second
	// HDMI changes this soon after a power change are part of it.
	notifyHDMIAfterPower = 2 * time.Minute
)

// debounce turns samples into changes: a new state counts once it has held
// for hold. The first sample is the baseline, not a change.
type debounce struct {
	hold    time.Duration
	known   bool
	state   bool
	pending bool
	since   time.Time
}

func (d *debounce) update(value bool, now time.Time) bool {
	if !d.known {
		d.known, d.state, d.pending = true, value, value
		return false
	}
	if value == d.state {
		d.pending = value
		return false
	}
	if value != d.pending {
		d.pending, d.since = value, now
		return false
	}
	if now.Sub(d.since) < d.hold {
		return false
	}
	d.state = value
	return true
}

func (d *debounce) reset() { d.known = false }

// hostWatch reports host power and HDMI signal changes to notify.
type hostWatch struct {
	wants func(notify.Kind) bool
	emit  func(notify.Event)
	// power reads the power LED; ok is false when it cannot be read or does
	// not count (ATX off, LED not wired).
	power func() (on, ok bool)
	// hdmi reads the signal; ok is false while capture is off.
	hdmi func() (signal, ok bool)

	powerState debounce
	hdmiState  debounce
	powerAt    time.Time // the last power change
	hdmiSince  time.Time // capture running since
}

func newHostWatch() *hostWatch {
	return &hostWatch{
		wants:      notify.Wants,
		emit:       notify.Emit,
		power:      notifyPowerLED,
		hdmi:       notifyHDMISignal,
		powerState: debounce{hold: notifyPowerHold},
		hdmiState:  debounce{hold: notifyHDMIHold},
	}
}

func (w *hostWatch) step(now time.Time) {
	w.stepPower(now)
	w.stepHDMI(now)
}

func (w *hostWatch) stepPower(now time.Time) {
	// The HDMI rule needs the power state even when power events are off.
	on, ok := false, w.wants(notify.KindPower) || w.wants(notify.KindHDMI)
	if ok {
		on, ok = w.power()
	}
	if !ok {
		// Starting afresh, a LED that never changes again does not count.
		w.powerState.reset()
		w.powerAt = time.Time{}
		return
	}
	if !w.powerState.update(on, now) {
		return
	}
	w.powerAt = now
	if !w.wants(notify.KindPower) {
		return
	}
	if on {
		w.emit(notify.Event{Kind: notify.KindPower, Severity: notify.SeverityInfo, Message: "power.on"})
	} else {
		w.emit(notify.Event{Kind: notify.KindPower, Severity: notify.SeverityWarning, Message: "power.off"})
	}
}

func (w *hostWatch) stepHDMI(now time.Time) {
	signal, ok := w.hdmi()
	if !ok || !w.wants(notify.KindHDMI) {
		w.hdmiState.reset()
		w.hdmiSince = time.Time{}
		return
	}
	if w.hdmiSince.IsZero() {
		w.hdmiSince = now
	}
	if now.Sub(w.hdmiSince) < notifyHDMISettle || !w.hdmiState.update(signal, now) {
		return
	}
	// A host that is off or has just changed its power sends no news by
	// losing or regaining its picture. Only a power LED seen to change
	// counts: one that is not wired reads off for good.
	if !w.powerAt.IsZero() && w.powerState.known && (!w.powerState.state || now.Sub(w.powerAt) < notifyHDMIAfterPower) {
		return
	}
	if signal {
		w.emit(notify.Event{Kind: notify.KindHDMI, Severity: notify.SeverityInfo, Message: "hdmi.restored"})
	} else {
		w.emit(notify.Event{Kind: notify.KindHDMI, Severity: notify.SeverityWarning, Message: "hdmi.lost"})
	}
}

var startNotifyWatchOnce sync.Once

// StartNotifyWatch starts watching the host for the notification events.
func StartNotifyWatch() {
	startNotifyWatchOnce.Do(func() {
		w := newHostWatch()
		go func() {
			ticker := time.NewTicker(notifyWatchInterval)
			defer ticker.Stop()
			for now := range ticker.C {
				w.step(now)
			}
		}()
	})
}

// The BMC page's "power LED connected" setting, reread this often.
const notifyLEDSettingInterval = 30 * time.Second

var (
	notifyLEDSettingAt   time.Time
	notifyLEDUnconnected bool
)

func notifyPowerLED() (bool, bool) {
	if !ATXEnabled() {
		return false, false
	}
	if now := time.Now(); now.Sub(notifyLEDSettingAt) >= notifyLEDSettingInterval {
		config, err := bmc.Load(bmc.ConfigPath)
		notifyLEDSettingAt, notifyLEDUnconnected = now, err == nil && config.PowerLEDDisconnected
	}
	if notifyLEDUnconnected {
		return false, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	leds, err := gpioMonitor().Current(ctx)
	if err != nil {
		return false, false
	}
	return leds.Power, true
}

// notifyHDMISignal is the signal while capture runs: switched off or
// stopped for idling, capture reads no signal whatever the host sends.
func notifyHDMISignal() (bool, bool) {
	if isHdmiDisabled() {
		return false, false
	}
	hdmiMutex.Lock()
	stopped := hdmiStoppedForIdle
	hdmiMutex.Unlock()
	if stopped {
		return false, false
	}
	return getHdmiSignal(), true
}
