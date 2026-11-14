// Package hostsensors holds what the managed computer reports about itself:
// temperatures, voltages, fan speeds and the like, read there by an agent
// (nanokvm-sensors) and sent to the NanoKVM over the USB serial port or the
// USB network. The computer is not trusted: a report is bounded and checked
// before anything of it is kept, and only the last one is kept.
package hostsensors

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// Version is the report format this server reads.
	Version = 1
	// MaxReportBytes bounds one report: a line on the serial port, a request
	// body on the network. A desktop with every sensor kind is about 40 KiB.
	MaxReportBytes = 256 << 10
	// MaxSensors bounds the readings of one report.
	MaxSensors  = 1024
	maxText     = 96
	maxInterval = 3600
	// A report older than three of the agent's intervals (and at least this)
	// is stale: the agent stopped, or the transport went away.
	minStaleAfter = 15 * time.Second
)

// Kinds and the unit each value is in. The agent converts; the server and
// the web interface never guess a unit.
var kinds = map[string]string{
	"temperature": "°C",
	"voltage":     "V",
	"current":     "A",
	"power":       "W",
	"fan":         "RPM",
	"control":     "%",
	"load":        "%",
	"level":       "%",
	"clock":       "MHz",
	"frequency":   "Hz",
	"data":        "GB",
	"smalldata":   "MB",
	"throughput":  "B/s",
	"energy":      "mWh",
	"humidity":    "%",
	"flow":        "L/h",
	"noise":       "dBA",
	"factor":      "",
}

// Unit is the unit of a kind, and whether the kind is known.
func Unit(kind string) (string, bool) {
	unit, ok := kinds[kind]
	return unit, ok
}

// Sensor is one reading.
type Sensor struct {
	// Hardware is the part the sensor belongs to ("AMD Ryzen 7 7800X3D").
	Hardware string `json:"hw"`
	// Type is the part's class: cpu, gpu, motherboard, memory, storage,
	// network, cooler, psu, battery, other.
	Type  string  `json:"type"`
	Name  string  `json:"name"`
	Kind  string  `json:"kind"`
	Value float64 `json:"value"`
}

// Report is one message of the agent.
type Report struct {
	Version int    `json:"v"`
	Host    string `json:"host"`
	OS      string `json:"os"`
	Agent   string `json:"agent"`
	// Interval is the agent's period in seconds.
	Interval int `json:"interval"`
	// Limited: the agent runs without the rights (or the driver) that most
	// board and processor sensors need.
	Limited bool     `json:"limited,omitempty"`
	Sensors []Sensor `json:"sensors"`
}

var hardwareTypes = map[string]bool{
	"cpu": true, "gpu": true, "motherboard": true, "memory": true, "storage": true,
	"network": true, "cooler": true, "psu": true, "battery": true, "other": true,
}

// clean accepts a short printable text: what the web interface shows as is.
func clean(field, s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", fmt.Errorf("%s: not UTF-8", field)
	}
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > maxText {
		return "", fmt.Errorf("%s: longer than %d characters", field, maxText)
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return "", fmt.Errorf("%s: a character that cannot be shown", field)
		}
	}
	return s, nil
}

// Parse reads and checks one report. A reading of an unknown kind is left
// out (a newer agent may know more kinds); anything else that is wrong
// rejects the whole report.
func Parse(data []byte) (*Report, error) {
	if len(data) > MaxReportBytes {
		return nil, errors.New("the report is too long")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	var r Report
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("not a report: %w", err)
	}
	if dec.More() {
		return nil, errors.New("text after the report")
	}
	if r.Version != Version {
		return nil, fmt.Errorf("report version %d, this server reads %d", r.Version, Version)
	}
	if len(r.Sensors) > MaxSensors {
		return nil, fmt.Errorf("more than %d sensors", MaxSensors)
	}
	if r.Interval < 1 || r.Interval > maxInterval {
		return nil, errors.New("interval out of range")
	}
	var err error
	if r.Host, err = clean("host", r.Host); err != nil {
		return nil, err
	}
	if r.OS, err = clean("os", r.OS); err != nil {
		return nil, err
	}
	if r.Agent, err = clean("agent", r.Agent); err != nil {
		return nil, err
	}
	kept := make([]Sensor, 0, len(r.Sensors))
	for _, s := range r.Sensors {
		if _, known := kinds[s.Kind]; !known {
			continue
		}
		if math.IsNaN(s.Value) || math.IsInf(s.Value, 0) {
			return nil, errors.New("a value that is not a number")
		}
		if s.Hardware, err = clean("hw", s.Hardware); err != nil {
			return nil, err
		}
		if s.Name, err = clean("name", s.Name); err != nil {
			return nil, err
		}
		if s.Name == "" {
			return nil, errors.New("a sensor without a name")
		}
		if !hardwareTypes[s.Type] {
			s.Type = "other"
		}
		kept = append(kept, s)
	}
	r.Sensors = kept
	return &r, nil
}

// Status is the last report and how it arrived.
type Status struct {
	Report *Report `json:"report"`
	// Transport: "serial" or "network"; "" before the first report.
	Transport string `json:"transport"`
	// Age of the report in milliseconds.
	Age int64 `json:"age"`
	// Stale: the agent has been silent for longer than three intervals.
	Stale bool `json:"stale"`
	// Rejected counts reports that failed the checks since the start, with
	// the last reason.
	Rejected   uint64 `json:"rejected"`
	LastReject string `json:"lastReject"`
}

// Store keeps the last report.
type Store struct {
	mu         sync.Mutex
	report     *Report
	transport  string
	at         time.Time
	peer       string
	rejected   uint64
	lastReject string
}

// Default is the server's store.
var Default = &Store{}

// Accept parses a report and keeps it; the error is also remembered for the
// status.
func (s *Store) Accept(data []byte, transport string, now time.Time) error {
	return s.AcceptFrom(data, transport, "", now)
}

// Last is when the last report came and, for the network, from which
// address; zero when there is none.
func (s *Store) Last() (at time.Time, peer string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.at, s.peer
}

// AcceptFrom is Accept with the address the report came from.
func (s *Store) AcceptFrom(data []byte, transport, peer string, now time.Time) error {
	r, err := Parse(data)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.rejected++
		s.lastReject = err.Error()
		return err
	}
	s.report, s.transport, s.at, s.peer = r, transport, now, peer
	return nil
}

// Clear forgets the report: the feature was turned off.
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.report, s.transport, s.at, s.peer = nil, "", time.Time{}, ""
}

// Status reports the last report as of now.
func (s *Store) Status(now time.Time) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{Report: s.report, Transport: s.transport, Rejected: s.rejected, LastReject: s.lastReject}
	if s.report == nil {
		return st
	}
	age := now.Sub(s.at)
	if age < 0 {
		age = 0
	}
	st.Age = age.Milliseconds()
	limit := 3 * time.Duration(s.report.Interval) * time.Second
	if limit < minStaleAfter {
		limit = minStaleAfter
	}
	st.Stale = age > limit
	return st
}
