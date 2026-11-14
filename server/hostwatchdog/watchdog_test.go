package hostwatchdog

import (
	"testing"
	"time"
)

// Silence makes the watchdog due only while nothing else explains it.
func TestWatchdogIsDueOnlyForAnUnexplainedSilence(t *testing.T) {
	t0 := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	at := func(minutes int) time.Time { return t0.Add(time.Duration(minutes) * time.Minute) }
	live := Signs{Watching: true, PowerOn: true, PowerKnown: true, Configured: true, Link: "1"}
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
		s.Report, s.Now = at(31), at(37)
		if !w.Step(s, 5*time.Minute) {
			t.Errorf("%s: a new report did not arm it again", name)
		}
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
