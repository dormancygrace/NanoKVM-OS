// Package standby implements storage-free network standby, not SoC power-off.
package standby

import (
	"errors"
	"sync"
	"time"
)

const (
	Directory = "/run/nkos-standby"
	Root      = Directory + "/root"
	Marker    = Directory + "/armed"
	Helper    = "/usr/sbin/nkos-standby"
)

type Config struct {
	Token         string
	Address       string
	ClientAddress string
	Prefix        int
	Gateway       string
	Interface     string
	MAC           string
	DHCP          bool
	WiFi          bool
	Hostname      string
	Port          int
	Certificate   []byte
	PrivateKey    []byte
	MagicWake     bool
}

type Status struct {
	State     string `json:"state"`
	Safe      bool   `json:"safe"`
	Error     string `json:"error,omitempty"`
	MAC       string `json:"mac"`
	MagicWake bool   `json:"magicWake"`
}

// State is also the restart serialization point. Wake requests during the
// transition are discarded, never carried into the safe state.
type State struct {
	mu      sync.Mutex
	status  Status
	readyAt time.Time
}

func NewState(c Config) *State {
	return &State{status: Status{State: "preparing", MAC: c.MAC, MagicWake: c.MagicWake}}
}
func (s *State) Get() Status { s.mu.Lock(); defer s.mu.Unlock(); return s.status }
func (s *State) Fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status.State == "failed" || s.status.State == "restarting" {
		return
	}
	s.status.State, s.status.Safe, s.status.Error = "failed", false, err.Error()
}

// Only the reboot operation itself may reopen a failed restart for retry.
func (s *State) RestartFailed(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status.State == "restarting" {
		s.status.State, s.status.Safe, s.status.Error = "failed", false, err.Error()
	}
}
func (s *State) Ready() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status.State == "preparing" {
		s.status.State, s.status.Safe = "ready", true
		s.readyAt = time.Now()
	}
}
func (s *State) Restart(magic bool) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.restartLocked(magic)
}
func (s *State) MagicRestart(received time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if received.IsZero() || received.Before(s.readyAt) {
		return false, errors.New("packet predates readiness")
	}
	return s.restartLocked(true)
}
func (s *State) restartLocked(magic bool) (bool, error) {
	if s.status.State == "restarting" {
		return false, nil
	}
	if s.status.State != "ready" && (magic || s.status.State != "failed") {
		return false, errors.New("shutdown is still being checked")
	}
	s.status.State, s.status.Safe = "restarting", false
	return true, nil
}

// Gate makes checking active requests and closing admission one atomic action.
// Background package workers and downloads additionally have cross-process locks.
type Gate struct {
	mu     sync.Mutex
	active int
	closed bool
}

func (g *Gate) Enter(mutating bool) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return false
	}
	if mutating {
		g.active++
	}
	return true
}
func (g *Gate) Leave(mutating bool) {
	if mutating {
		g.mu.Lock()
		g.active--
		g.mu.Unlock()
	}
}
func (g *Gate) Begin() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return errors.New("shutdown already requested")
	}
	if g.active != 0 {
		return errors.New("another operation is running; finish it before shutdown")
	}
	g.closed = true
	return nil
}
func (g *Gate) Cancel() { g.mu.Lock(); g.closed = false; g.mu.Unlock() }

var Requests Gate
