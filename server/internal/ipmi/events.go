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

// event hands e to OnEvent off the packet path; s.mu is held.
func (s *Server) event(e Event) {
	if s.OnEvent != nil {
		go s.OnEvent(e)
	}
}
