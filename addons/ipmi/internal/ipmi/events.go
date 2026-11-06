package ipmi

import "time"

// Event is a login or a lockout, for the security notifications the server
// sends for the web and Redfish logins too. The server reads them through
// the add-on's API socket (GET /events).
type Event struct {
	Seq  uint64 `json:"seq"`
	Kind string `json:"kind"` // "login" or "lockout"
	// User is the IPMI user name of a login.
	User    string `json:"user,omitempty"`
	Address string `json:"address"`
	// Failures and Minutes describe a lockout: the logins counted, and how
	// long further ones are refused.
	Failures int       `json:"failures,omitempty"`
	Minutes  int       `json:"minutes,omitempty"`
	At       time.Time `json:"at"`
}

// maxEvents is the backlog kept for the server, which reads it every few
// seconds; older events are dropped.
const maxEvents = 32

// event records e; s.mu is held.
func (s *Server) event(e Event) {
	s.eventSeq++
	e.Seq = s.eventSeq
	e.At = s.now()
	if len(s.events) == maxEvents {
		copy(s.events, s.events[1:])
		s.events = s.events[:maxEvents-1]
	}
	s.events = append(s.events, e)
}

// Events returns the events after sequence number after, oldest first, and
// the newest sequence number.
func (s *Server) Events(after uint64) ([]Event, uint64) {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Event
	for _, e := range s.events {
		if e.Seq > after {
			out = append(out, e)
		}
	}
	return out, s.eventSeq
}
