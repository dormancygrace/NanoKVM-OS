// Package hostwatchdog decides when the host has hung: the watchdog a
// server's BMC has (iLO's ASR, IPMI's watchdog timer), without a hardware
// timer the host's system resets.
//
// What the NanoKVM has is the reports of the computer sensors agent, which
// runs in the host's system. Their silence alone is not a hang: the agent
// may have been stopped or updated, the host may have been restarted into
// its firmware or an installer, put to sleep or switched off. So silence
// only makes the watchdog due, and it is due only while nothing else says
// what the silence is:
//
//   - a report armed it, in this run of the server and since the host's USB
//     link came up: a host that never ran the agent is never watched;
//   - the host's power is on (when the power LED tells);
//   - the host's side of the USB link is configured and is the same link
//     the last report came over. A restart of the host, a firmware setup, an
//     installer and a replug all enumerate the USB devices anew; sleep
//     suspends them. A hung system does neither.
//
// When it is due, the caller asks the host itself (an ARP request on the
// USB network, which a living kernel answers whatever its firewall), and
// acts only on no answer. One decision per silence: after it the watchdog
// waits for the next report.
package hostwatchdog

import (
	"encoding/json"
	"errors"
	"os"
	"time"

	"NanoKVM-Server/internal/atomicfile"
)

const (
	Off    = "off"
	Notify = "notify" // tell, press nothing
	Reset  = "reset"  // press the reset button
	Cycle  = "cycle"  // power off, then on

	DefaultMinutes = 5
	MinMinutes     = 2
	MaxMinutes     = 60
)

// Config is the setting: what to do and after how long a silence.
type Config struct {
	Action  string `json:"action"`
	Minutes int    `json:"minutes"`
}

func (c Config) Validate() error {
	switch c.Action {
	case Off, Notify, Reset, Cycle:
	default:
		return errors.New("action is off, notify, reset or cycle")
	}
	if c.Minutes < MinMinutes || c.Minutes > MaxMinutes {
		return errors.New("the silence is 2 to 60 minutes")
	}
	return nil
}

// Load reads the setting; a missing or unreadable one is off.
func Load(path string) Config {
	off := Config{Action: Off, Minutes: DefaultMinutes}
	data, err := os.ReadFile(path)
	if err != nil {
		return off
	}
	var c Config
	if json.Unmarshal(data, &c) != nil || c.Validate() != nil {
		return off
	}
	return c
}

func Save(path string, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	data, _ := json.Marshal(c)
	return atomicfile.Write(path, append(data, '\n'), 0o644)
}

// Signs is what the NanoKVM sees of the host at one moment.
type Signs struct {
	Now time.Time
	// Watching: the watchdog is on and the agent reports over the USB
	// network.
	Watching bool
	// Report is when the last report came, in this run of the server; zero
	// when none did.
	Report time.Time
	// PowerOn counts when PowerKnown (the power LED is wired and read).
	PowerOn, PowerKnown bool
	// Configured: the host's side of the USB link is configured (not
	// suspended, not detached). Link names the link's life: another value
	// means the USB devices were enumerated anew.
	Configured bool
	Link       string
}

// Watchdog is the state between steps.
type Watchdog struct {
	armed   bool
	decided bool
	report  time.Time
	link    string
}

// Armed reports whether a silence would now make the watchdog due.
func (w *Watchdog) Armed() bool { return w.armed && !w.decided }

// Step takes the signs and reports whether the watchdog is due: the agent
// has been silent for silence and nothing explains it. The caller then
// decides (asking the host) and calls Decided.
func (w *Watchdog) Step(s Signs, silence time.Duration) bool {
	if !s.Watching || !s.Configured || (s.PowerKnown && !s.PowerOn) {
		w.armed = false
		return false
	}
	if s.Report.After(w.report) {
		w.report, w.link, w.armed, w.decided = s.Report, s.Link, true, false
	}
	if !w.armed || w.decided {
		return false
	}
	if s.Link != w.link {
		// Enumerated anew since the report: the host restarted, or was
		// plugged in again. Its next report arms again.
		w.armed = false
		return false
	}
	return s.Now.Sub(w.report) >= silence
}

// Decided ends this silence: nothing more happens until a report.
func (w *Watchdog) Decided() { w.decided = true }
