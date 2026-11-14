package hostwatchdog

import (
	"testing"
	"time"
)

// Silence makes the watchdog due only while nothing else explains it.
func TestWatchdogIsDueOnlyForAnUnexplainedSilence(t *testing.T) {
	t0 := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	at := func(minutes int) time.Time { return t0.Add(time.Duration(minutes) * time.Minute) }
	live := Signs{Watching: true, PowerOn: true, PowerKnown: true, Configured: true, Link: "1", ReportLink: "1"}
	for name, c := range map[string]struct {
		change func(*Signs) // what is different at the time of the silence
		due    bool
	}{
		"a hung host":                 {func(*Signs) {}, true},
		"the power LED not wired":     {func(s *Signs) { s.PowerKnown, s.PowerOn = false, false }, true},
		"the host switched off":       {func(s *Signs) { s.PowerOn = false }, false},
		"asleep or unplugged":         {func(s *Signs) { s.Configured = false }, false},
		"restarted into its firmware": {func(s *Signs) { s.Link = "2" }, false},
		"the watchdog switched off":   {func(s *Signs) { s.Watching = false }, false},
	} {
		var w Watchdog
		s := live
		s.Now = at(0)
		if w.Step(s, 5*time.Minute) || w.Armed() {
			t.Fatalf("%s: due or armed before any report", name)
		}
		s.Report = at(1)
		s.Now = at(5)
		if w.Step(s, 5*time.Minute) || !w.Armed() {
			t.Fatalf("%s: due 4 min after a report, or not armed by it", name)
		}
		c.change(&s)
		s.Now = at(7)
		if got := w.Step(s, 5*time.Minute); got != c.due {
			t.Errorf("%s: due %v, want %v", name, got, c.due)
		}
		// Asked again with the same signs, a due decision holds; any of the
		// explanations arriving meanwhile, or a report, ends it.
		for _, later := range []func(*Signs){func(*Signs) {}, func(s *Signs) { s.Watching = false }, func(s *Signs) { s.Link = "9" },
			func(s *Signs) { s.Configured = false }, func(s *Signs) { s.Report = at(7) }, func(s *Signs) { s.Link = "" }} {
			again := s
			later(&again)
			if got := w.StillDue(again, 5*time.Minute); got != (c.due && again == s) {
				t.Errorf("%s: still due %v with %+v", name, got, again)
			}
		}
		// The silence set longer meanwhile: not due under it.
		if w.StillDue(s, 60*time.Minute) {
			t.Errorf("%s: still due under a silence of an hour after 6 minutes", name)
		}
		if !c.due {
			// What explained the silence is over: still nothing without a report.
			s = live
			s.Report, s.Now = at(1), at(20)
			if name != "the host switched off" && name != "asleep or unplugged" && name != "the watchdog switched off" {
				s.Link = "2"
			}
			if w.Step(s, 5*time.Minute) {
				t.Errorf("%s: due afterwards without a new report", name)
			}
			continue
		}
		w.Decided()
		s.Now = at(30)
		if w.Step(s, 5*time.Minute) {
			t.Errorf("%s: due a second time for one silence", name)
		}
		// A decision taken back is made anew, under the silence set then.
		w.Reopen()
		if w.Step(s, 60*time.Minute) || !w.Step(s, 5*time.Minute) {
			t.Errorf("%s: a reopened decision is not made anew under the setting of then", name)
		}
		w.Decided()
		s.Report, s.Now = at(31), at(37)
		if !w.Step(s, 5*time.Minute) {
			t.Errorf("%s: a new report did not arm it again", name)
		}
	}
}

// A report that came over the link before a new enumeration is no evidence
// for the link after it, whenever the watchdog first sees it.
func TestAReportArmsOnlyTheLinkItCameOver(t *testing.T) {
	t0 := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	var w Watchdog
	s := Signs{Watching: true, Configured: true, Link: "180", ReportLink: "180", Report: t0, Now: t0}
	w.Step(s, time.Minute)
	// A second report over 180, then the host restarts (184) before the
	// watchdog's next look.
	s.Report, s.Link, s.Now = t0.Add(time.Second), "184", t0.Add(5*time.Second)
	if w.Step(s, time.Minute) || w.Armed() {
		t.Fatal("armed for the new link by a report of the old one")
	}
	s.Now = t0.Add(10 * time.Minute)
	if w.Step(s, time.Minute) {
		t.Error("due on a link no report came over")
	}
	// Without a readable link nothing is decided.
	s.Report, s.ReportLink, s.Link = t0.Add(11*time.Minute), "", ""
	s.Now = t0.Add(20 * time.Minute)
	if w.Step(s, time.Minute) {
		t.Error("due without a readable link")
	}
}

func TestConfig(t *testing.T) {
	path := t.TempDir() + "/watchdog.json"
	if c := Load(path); c.Action != Off || c.Minutes != DefaultMinutes {
		t.Fatalf("no file: %+v", c)
	}
	if err := Save(path, Config{Action: Reset, Minutes: 1}); err == nil {
		t.Error("a silence of one minute was saved")
	}
	if err := Save(path, Config{Action: Reset, Minutes: 10}); err != nil || Load(path) != (Config{Action: Reset, Minutes: 10}) {
		t.Errorf("saved %v, loaded %+v", err, Load(path))
	}
}
