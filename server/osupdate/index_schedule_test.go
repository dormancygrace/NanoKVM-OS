package osupdate

import (
	"strings"
	"testing"
)

func TestIndexScheduleDefaultAndValidation(t *testing.T) {
	s, err := parseIndexSchedule(nil)
	if err != nil || s.Enabled || s.Time != "03:00" {
		t.Fatalf("schedule must default off: %+v %v", s, err)
	}
	for _, value := range []string{"00:00", "03:05", "23:59"} {
		if _, _, err := validateIndexSchedule(IndexSchedule{Time: value}); err != nil {
			t.Fatalf("valid time %q: %v", value, err)
		}
	}
	for _, value := range []string{"", "3:00", "24:00", "23:60", "aa:bb", "-1:00", "03:00; reboot", "03:00\n"} {
		if _, err := renderIndexSchedule(nil, IndexSchedule{Enabled: true, Time: value}); err == nil {
			t.Fatalf("invalid cron time accepted: %q", value)
		}
	}
}

func TestIndexSchedulePreservesOtherCronJobsAndDisables(t *testing.T) {
	original := "PATH=/usr/sbin:/usr/bin:/sbin:/bin\n# user job\n5 4 * * * /usr/bin/custom-job\n"
	s := IndexSchedule{Enabled: true, Time: "06:17"}
	enabled, err := renderIndexSchedule([]byte(original), s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(enabled), original) || !strings.Contains(string(enabled), "17 6 * * * "+indexRefreshCommand) {
		t.Fatalf("other jobs changed or wrong time: %s", enabled)
	}
	roundtrip, err := parseIndexSchedule(enabled)
	if err != nil || roundtrip != s {
		t.Fatalf("round trip: %+v %v", roundtrip, err)
	}
	again, _ := renderIndexSchedule(enabled, s)
	if string(again) != string(enabled) {
		t.Fatal("saving the same schedule duplicated the job")
	}
	s.Enabled = false
	disabled, _ := renderIndexSchedule(enabled, s)
	if !strings.HasPrefix(string(disabled), original) || strings.Contains(string(disabled), indexRefreshCommand) {
		t.Fatalf("disabled schedule left a runnable job or removed user jobs: %s", disabled)
	}
	roundtrip, err = parseIndexSchedule(disabled)
	if err != nil || roundtrip != s {
		t.Fatalf("disabled time must be retained: %+v %v", roundtrip, err)
	}
}
