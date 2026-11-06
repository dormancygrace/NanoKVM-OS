package ipmi

import "testing"

// A login and the lockout that follows each give one event; the refused
// logins after the lockout give none.
func TestLoginAndLockoutEvents(t *testing.T) {
	s := newServer(&fakePower{})
	c := &console{t: t, s: s, su: suites[0], user: "admin", password: "correct-horse"}
	if status := c.login(); status != statusOK {
		t.Fatalf("login status %#x", status)
	}
	for i := 0; i < maxFailures+3; i++ {
		c := &console{t: t, s: s, su: suites[0], user: "admin", password: "guess"}
		c.login()
	}
	events, latest := s.Events(0)
	if len(events) != 2 || latest != 2 {
		t.Fatalf("events %+v, latest %d", events, latest)
	}
	host := clientKey(addr)
	if e := events[0]; e.Kind != "login" || e.User != "admin" || e.Address != hostOf(addr) || e.Seq != 1 {
		t.Fatalf("login event %+v", e)
	}
	if e := events[1]; e.Kind != "lockout" || e.Address != host || e.Attempts != maxFailures || e.Minutes != 1 {
		t.Fatalf("lockout event %+v", e)
	}
	if later, _ := s.Events(2); len(later) != 0 {
		t.Fatalf("events after 2: %+v", later)
	}
}

// A login names the host, not the /64 the lockout counts: two hosts of one
// network are two addresses for the new-address notification.
func TestLoginAddressIsTheHost(t *testing.T) {
	a, b := "[2001:db8:1234:5678::1]:623", "[2001:db8:1234:5678::2]:623"
	if hostOf(a) == hostOf(b) || hostOf(a) != "2001:db8:1234:5678::1" {
		t.Fatalf("hosts %q %q", hostOf(a), hostOf(b))
	}
	if clientKey(a) != clientKey(b) {
		t.Fatalf("rate-limit keys %q %q", clientKey(a), clientKey(b))
	}
}
