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
