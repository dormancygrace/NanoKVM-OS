package osupdate

import (
	"errors"
	"os"
	"path/filepath"
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

func TestIndexScheduleUsesCrontabAndReportsFailures(t *testing.T) {
	oldPath, oldExec := indexCrontabPath, indexScheduleExec
	t.Cleanup(func() { indexCrontabPath, indexScheduleExec = oldPath, oldExec })
	indexCrontabPath = filepath.Join(t.TempDir(), "root")
	original := "1 2 * * * /usr/bin/keep-me\n"
	if err := os.WriteFile(indexCrontabPath, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	var commands []string
	indexScheduleExec = func(input []byte, name string, args ...string) ([]byte, error) {
		commands = append(commands, name+" "+strings.Join(args, " "))
		if name == "crontab" {
			if !strings.HasPrefix(string(input), original) {
				t.Fatal("lost existing jobs")
			}
			return nil, os.WriteFile(indexCrontabPath, input, 0600)
		}
		return nil, nil
	}
	want := IndexSchedule{Enabled: true, Time: "21:42"}
	if err := SetIndexSchedule(want); err != nil {
		t.Fatal(err)
	}
	got, err := GetIndexSchedule()
	if err != nil || got != want {
		t.Fatalf("persisted setting: %+v %v", got, err)
	}
	if strings.Join(commands, ";") != "rc-update add crond default;rc-service crond status;crontab -" {
		t.Fatalf("unexpected side effects: %v", commands)
	}
	indexScheduleExec = func([]byte, string, ...string) ([]byte, error) {
		return nil, errors.New("write denied")
	}
	if err := SetIndexSchedule(IndexSchedule{Time: "21:42"}); err == nil {
		t.Fatal("crontab write failure was hidden")
	}
	got, _ = GetIndexSchedule()
	if got != want {
		t.Fatal("failed save changed the setting")
	}
}

func TestSoftwareRefreshOnlyUpdatesIndexes(t *testing.T) {
	oldDir, oldExec := apkStateDir, apkExecutable
	t.Cleanup(func() { apkStateDir, apkExecutable = oldDir, oldExec })
	apkStateDir = t.TempDir()
	apkExecutable = filepath.Join(t.TempDir(), "apk")
	script := "#!/bin/sh\n[ \"$#\" -eq 1 ] && [ \"$1\" = update ]\n"
	if err := os.WriteFile(apkExecutable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := RunSoftware("refresh", ""); err != nil {
		t.Fatalf("refresh tried an install/upgrade or failed: %v", err)
	}
	if status := GetSoftwareStatus(); status.State != "succeeded" || status.Action != "refresh" {
		t.Fatalf("refresh status not visible: %+v", status)
	}
}
