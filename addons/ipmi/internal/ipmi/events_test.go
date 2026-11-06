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
	if e := events[0]; e.Kind != "login" || e.User != "admin" || e.Address != host || e.Seq != 1 {
		t.Fatalf("login event %+v", e)
	}
	if e := events[1]; e.Kind != "lockout" || e.Address != host || e.Failures != maxFailures || e.Minutes != 1 {
		t.Fatalf("lockout event %+v", e)
	}
	if later, _ := s.Events(2); len(later) != 0 {
		t.Fatalf("events after 2: %+v", later)
	}
}
