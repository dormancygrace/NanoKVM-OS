package vm

import (
	"strings"
	"testing"
	"time"
)

// Next runs as BusyBox crond would start them, and expressions it rejects.
func TestCronNext(t *testing.T) {
	from := time.Date(2026, 11, 15, 10, 7, 30, 0, time.UTC) // a Sunday
	for expr, want := range map[string]string{
		"*/15 * * * *":      "10:15 10:30 10:45",
		"30 2 * * 1,3":      "11-16 02:30, 11-18 02:30, 11-23 02:30",
		"0 9 1 * *":         "12-01 09:00, 01-01 09:00, 02-01 09:00",
		"0 0 13 * fri":      "11-20 00:00, 11-27 00:00, 12-04 00:00", // day 13 or any Friday
		"5 10-11/1 * jan *": "01-01 10:05, 01-01 11:05, 01-02 10:05",
	} {
		s, err := parseCron(expr)
		if err != nil {
			t.Fatal(expr, err)
		}
		var got []string
		for _, r := range s.next(from, 3) {
			if strings.Contains(want, ",") {
				got = append(got, r.Format("01-02 15:04"))
			} else {
				got = append(got, r.Format("15:04"))
			}
		}
		if sep := map[bool]string{true: ", ", false: " "}[strings.Contains(want, ",")]; strings.Join(got, sep) != want {
			t.Errorf("%s: %v", expr, got)
		}
	}
	for _, bad := range []string{"* * * *", "60 * * * *", "* * * * 7", "*/0 * * * *", "5-1 * * * *", "@daily x y z w", "L * * * *"} {
		if _, err := parseCron(bad); err == nil {
			t.Error("accepted", bad)
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
