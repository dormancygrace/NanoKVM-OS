package ipmi

// Event is a login or a lockout, for the security notifications the server
// sends for the web and Redfish logins too.
type Event struct {
	Kind string // "login" or "lockout"
	// User is the IPMI user name of a login.
	User    string
	Address string
	// Attempts and Minutes describe a lockout: the logins counted (failed
	// or not; each hands out a password hash), and how long further ones
	// are refused. A lockout's address is the rate-limit key: an IPv6 /64.
	Attempts int
	Minutes  int
}

// maxEvents is how many events wait for OnEvent; beyond that an event is
// dropped and logged.
const maxEvents = 32

// event queues e for OnEvent, off the packet path; s.mu is held.
func (s *Server) event(e Event) {
	if s.OnEvent == nil {
		return
	}
	select {
	case s.events <- e:
	default:
		s.logf("IPMI: notification queue full, %s from %s not reported", e.Kind, e.Address)
	}
}

// notify hands e to OnEvent; a panic there is logged.
func (s *Server) notify(e Event) {
	defer func() {
		if r := recover(); r != nil {
			s.logf("IPMI notification failed: %v", r)
		}
	}()
	s.OnEvent(e)
}
