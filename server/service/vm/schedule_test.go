package vm

import (
	"strings"
	"testing"
	"time"
)

// Next runs as BusyBox crond 1.38 starts them (ParseField, FixDayDow), and
// expressions it cannot read.
func TestCronNext(t *testing.T) {
	from := time.Date(2026, 11, 15, 10, 7, 30, 0, time.UTC) // a Sunday
	for expr, want := range map[string]string{
		"*/15 * * * *":      "11-15 10:15, 11-15 10:30, 11-15 10:45",
		"30 2 * * 1,3":      "11-16 02:30, 11-18 02:30, 11-23 02:30",
		"0 0 13 * fri":      "11-20 00:00, 11-27 00:00, 12-04 00:00", // either day field
		"0 0 13 * 0-6":      "12-13 00:00, 01-13 00:00, 02-13 00:00", // every weekday: the date alone
		"0 0 */2 * *":       "11-16 00:00, 11-18 00:00, 11-20 00:00", // steps from index 0: even dates
		"0 0 * * */2":       "11-17 00:00, 11-19 00:00, 11-21 00:00", // Sun, Tue, Thu, Sat
		"0 22-1 * * *":      "11-15 22:00, 11-15 23:00, 11-16 00:00", // a range wraps
		"5 10-11/1 * jan *": "01-01 10:05, 01-01 11:05, 01-02 10:05",
	} {
		s, err := parseCron(expr)
		if err != nil {
			t.Fatal(expr, err)
		}
		var got []string
		for _, r := range s.next(from, 3) {
			got = append(got, r.Format("01-02 15:04"))
		}
		if strings.Join(got, ", ") != want {
			t.Errorf("%s: %v", expr, got)
		}
	}
	for _, bad := range []string{"* * * *", "60 * * * *", "0 +3 * * *", "* * * * 7", "* * * 0 *", "*/0 * * * *", "@daily x y z w", "L * * * *", "1,x * * * *"} {
		if _, err := parseCron(bad); err == nil {
			t.Error("accepted", bad)
		}
	}
	// A DST gap: 02:00-03:00 does not exist on 2027-03-14 in New York.
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no time zone data")
	}
	for expr, want := range map[string]string{"0 3 * * *": "03-14 03:00", "30 2 * * *": "03-15 02:30"} {
		s, _ := parseCron(expr)
		if runs := s.next(time.Date(2027, 3, 14, 0, 0, 0, 0, ny), 1); len(runs) != 1 || runs[0].Format("01-02 15:04") != want {
			t.Errorf("%s across DST: %v", expr, runs)
		}
	}
	// The autumn hour comes twice (Berlin, 2026-10-25): both 02:30s, CEST first.
	if berlin, err := time.LoadLocation("Europe/Berlin"); err == nil {
		s, _ := parseCron("30 2 * * *")
		runs := s.next(time.Date(2026, 10, 25, 0, 0, 0, 0, berlin), 2)
		if len(runs) != 2 || runs[0].UTC().Format("15:04") != "00:30" || runs[1].UTC().Format("15:04") != "01:30" {
			t.Errorf("repeated hour: %v", runs)
		}
	}
	// Changes at :45 (Pacific/Chatham): a skipped hour stops at the change.
	if chatham, err := time.LoadLocation("Pacific/Chatham"); err == nil {
		for expr, c := range map[string]struct {
			from time.Time
			want string
		}{
			"50 3 * * *": {time.Date(2027, 9, 26, 0, 0, 0, 0, chatham), "09-26 03:50 +1345"},
			// From 03:00 +13:45, before the 03:45 change back to 02:45 +12:45.
			"50 2 * * *": {time.Date(2027, 4, 4, 0, 0, 0, 0, chatham).Add(3 * time.Hour), "04-04 02:50 +1245"},
		} {
			s, _ := parseCron(expr)
			if runs := s.next(c.from, 1); len(runs) != 1 || runs[0].Format("01-02 15:04 -0700") != c.want {
				t.Errorf("%s in Chatham: %v", expr, runs)
			}
		}
	}
}

// next agrees with a scan of every minute of a year, in zones whose offset
// changes on the hour, at :30 and at :45. A regression guard: the DST skip
// came back twice after its first fix (7a8e51d5, a gap looped for ever):
// f03ebba7 jumped over the first of a repeated hour, 9cb7765c over a :45
// change in Pacific/Chatham.
func TestCronNextAgainstScan(t *testing.T) {
	for _, zone := range []string{"Pacific/Chatham", "Australia/Lord_Howe", "Europe/Berlin", "America/New_York"} {
		loc, err := time.LoadLocation(zone)
		if err != nil {
			t.Skip("no time zone data")
		}
		for _, expr := range []string{"50 3 * * *", "30 2 * * *", "*/7 1-3 * * *", "45 2 * 4,9,10 *"} {
			s, _ := parseCron(expr)
			from := time.Date(2027, 1, 1, 0, 0, 0, 0, loc)
			got := s.next(from, 40)
			var want []time.Time
			for m := from.Add(time.Minute); len(want) < len(got) && m.Before(from.AddDate(1, 0, 0)); m = m.Add(time.Minute) {
				if s.matches(m) {
					want = append(want, m)
				}
			}
			for i := range got {
				if i >= len(want) || !got[i].Equal(want[i]) {
					t.Errorf("%s %s: run %d is %v, a minute scan gives %v", zone, expr, i, got[i], want)
					break
				}
			}
		}
	}
}

// Our tasks round-trip; other lines stay; a quote cannot leave its word.
func TestTasksInCrontab(t *testing.T) {
	other := "0 2 * * * run-parts /etc/periodic/daily\n# nanokvm-apk-index-refresh {\"enabled\":false}\n"
	tasks := []ScheduledTask{{ID: "t0123abcd", Name: "a", Enabled: true, Schedule: "*/5 * * * *", Kind: "command", Target: "echo 'x'; date"},
		{ID: "t89ab0123", Name: "b", Schedule: "0 3 * * *", Kind: "script", Target: "b.py"}}
	_, lines := parseTasks([]byte(other))
	data := renderTasks(tasks, lines)
	got, kept := parseTasks(data)
	if len(got) != 2 || got[0] != tasks[0] || got[1] != tasks[1] || strings.Join(kept, "\n")+"\n" != other {
		t.Fatalf("%q", data)
	}
	if !strings.Contains(string(data), `'/bin/sh' '-c' 'echo '\''x'\''; date' # nanokvm-task t0123abcd`) || strings.Count(string(data), "run-task") != 1 {
		t.Errorf("%q", data)
	}
}
