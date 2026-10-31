package vm

import (
	"testing"
	"time"

	"NanoKVM-Server/service/notify"
)

type watchedHost struct {
	power, powerOK, signal, hdmiOK bool
	wants                          map[notify.Kind]bool
	events                         []string
}

func (h *watchedHost) watch() *hostWatch {
	return &hostWatch{
		wants:      func(k notify.Kind) bool { return h.wants[k] },
		emit:       func(e notify.Event) { h.events = append(h.events, e.Message) },
		power:      func() (bool, bool) { return h.power, h.powerOK },
		hdmi:       func() (bool, bool) { return h.signal, h.hdmiOK },
		powerState: debounce{hold: notifyPowerHold},
		hdmiState:  debounce{hold: notifyHDMIHold},
	}
}

// runWatch steps the watch once a second for d.
func runWatch(w *hostWatch, now *time.Time, d time.Duration) {
	for end := now.Add(d); now.Before(end); *now = now.Add(time.Second) {
		w.step(*now)
	}
}

func TestDebounce(t *testing.T) {
	d := debounce{hold: 3 * time.Second}
	at := func(s int) time.Time { return time.Unix(int64(s), 0) }
	if d.update(true, at(0)) {
		t.Fatal("the baseline counted as a change")
	}
	// A blink shorter than the hold is not a change.
	if d.update(false, at(1)) || d.update(true, at(2)) || d.update(false, at(3)) || d.update(false, at(5)) {
		t.Fatal("a short blink counted")
	}
	if !d.update(false, at(6)) {
		t.Fatal("a held change did not count")
	}
	if d.update(false, at(7)) {
		t.Fatal("counted twice")
	}
}

func TestHostWatchPower(t *testing.T) {
	h := &watchedHost{power: true, powerOK: true, wants: map[notify.Kind]bool{notify.KindPower: true}}
	w := h.watch()
	now := time.Unix(0, 0)
	runWatch(w, &now, 10*time.Second)
	if len(h.events) != 0 {
		t.Fatalf("baseline: %v", h.events)
	}
	// A sleeping host blinks its LED: no events.
	for i := 0; i < 20; i++ {
		h.power = i%4 < 2
		runWatch(w, &now, time.Second)
	}
	h.power = false
	runWatch(w, &now, 10*time.Second)
	h.power = true
	runWatch(w, &now, 10*time.Second)
	if len(h.events) != 2 || h.events[0] != "power.off" || h.events[1] != "power.on" {
		t.Fatalf("events %v", h.events)
	}
	// Unreadable (ATX switched off) starts afresh: no event for the gap.
	h.powerOK = false
	runWatch(w, &now, 5*time.Second)
	h.powerOK, h.power = true, false
	runWatch(w, &now, 10*time.Second)
	if len(h.events) != 2 {
		t.Fatalf("events after the gap %v", h.events)
	}
}

func TestHostWatchHDMI(t *testing.T) {
	h := &watchedHost{signal: true, hdmiOK: true, wants: map[notify.Kind]bool{notify.KindHDMI: true}}
	w := h.watch()
	now := time.Unix(0, 0)
	runWatch(w, &now, 20*time.Second)
	// A mode switch drops the signal for a moment.
	h.signal = false
	runWatch(w, &now, 5*time.Second)
	h.signal = true
	runWatch(w, &now, 20*time.Second)
	if len(h.events) != 0 {
		t.Fatalf("short drop: %v", h.events)
	}
	h.signal = false
	runWatch(w, &now, 15*time.Second)
	if len(h.events) != 1 || h.events[0] != "hdmi.lost" {
		t.Fatalf("lost: %v", h.events)
	}

	// Capture stopped and started again: the picture of a starting capture
	// is not a change.
	h.hdmiOK = false
	runWatch(w, &now, 5*time.Second)
	h.hdmiOK, h.signal = true, false
	runWatch(w, &now, 5*time.Second)
	h.signal = true
	runWatch(w, &now, 30*time.Second)
	if len(h.events) != 1 {
		t.Fatalf("after capture restart: %v", h.events)
	}
}

func TestHostWatchHDMIFollowsPower(t *testing.T) {
	h := &watchedHost{power: true, powerOK: true, signal: true, hdmiOK: true,
		wants: map[notify.Kind]bool{notify.KindHDMI: true}}
	w := h.watch()
	now := time.Unix(0, 0)
	runWatch(w, &now, 20*time.Second)
	// Powered off: the lost picture is part of it.
	h.power, h.signal = false, false
	runWatch(w, &now, 30*time.Second)
	// Powered on, the picture comes back during boot.
	h.power = true
	runWatch(w, &now, 20*time.Second)
	h.signal = true
	runWatch(w, &now, 20*time.Second)
	if len(h.events) != 0 {
		t.Fatalf("events around power changes: %v", h.events)
	}
	// Long after, a lost picture is news.
	runWatch(w, &now, notifyHDMIAfterPower)
	h.signal = false
	runWatch(w, &now, 15*time.Second)
	if len(h.events) != 1 || h.events[0] != "hdmi.lost" {
		t.Fatalf("events %v", h.events)
	}
}

// ATX on with the power LED not wired: it reads off for good, and must not
// silence the HDMI events.
func TestHostWatchHDMIWithUnwiredLED(t *testing.T) {
	h := &watchedHost{power: false, powerOK: true, signal: true, hdmiOK: true,
		wants: map[notify.Kind]bool{notify.KindPower: true, notify.KindHDMI: true}}
	w := h.watch()
	now := time.Unix(0, 0)
	runWatch(w, &now, 20*time.Second)
	h.signal = false
	runWatch(w, &now, 15*time.Second)
	if len(h.events) != 1 || h.events[0] != "hdmi.lost" {
		t.Fatalf("events %v", h.events)
	}

	// The LED once changed, then ATX went off and on with the LED reading
	// off for good: that old change no longer silences HDMI.
	h.power = true
	runWatch(w, &now, 10*time.Second)
	h.powerOK = false
	runWatch(w, &now, 5*time.Second)
	h.powerOK, h.power = true, false
	runWatch(w, &now, notifyHDMIAfterPower)
	h.signal = true
	runWatch(w, &now, 15*time.Second)
	if len(h.events) != 3 || h.events[2] != "hdmi.restored" {
		t.Fatalf("events after ATX off and on %v", h.events)
	}
}
