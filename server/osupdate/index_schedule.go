package osupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const indexScheduleMarker = "# nanokvm-apk-index-refresh"
const indexRefreshCommand = "/usr/sbin/nkos-update refresh-indexes"

var indexScheduleMu sync.Mutex
var indexCrontabPath = "/etc/crontabs/root"
var indexScheduleExec = func(input []byte, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = bytes.NewReader(input)
	return cmd.CombinedOutput()
}

type IndexSchedule struct {
	Enabled bool   `json:"enabled"`
	Time    string `json:"time"`
}

func validateIndexSchedule(s IndexSchedule) (int, int, error) {
	if len(s.Time) != 5 || s.Time[2] != ':' {
		return 0, 0, errors.New("time must use HH:MM format")
	}
	for _, i := range []int{0, 1, 3, 4} {
		if s.Time[i] < '0' || s.Time[i] > '9' {
			return 0, 0, errors.New("time must use HH:MM format")
		}
	}
	hour, _ := strconv.Atoi(s.Time[:2])
	minute, _ := strconv.Atoi(s.Time[3:])
	if hour > 23 || minute > 59 {
		return 0, 0, errors.New("time must be between 00:00 and 23:59")
	}
	return hour, minute, nil
}

func parseIndexSchedule(data []byte) (IndexSchedule, error) {
	s := IndexSchedule{Time: "03:00"}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, indexScheduleMarker+" ") {
			continue
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, indexScheduleMarker+" ")), &s); err != nil {
			return s, fmt.Errorf("read index refresh schedule: %w", err)
		}
		if _, _, err := validateIndexSchedule(s); err != nil {
			return s, err
		}
		break
	}
	return s, nil
}

func renderIndexSchedule(data []byte, s IndexSchedule) ([]byte, error) {
	hour, minute, err := validateIndexSchedule(s)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if strings.HasPrefix(line, indexScheduleMarker+" ") || strings.HasSuffix(line, " "+indexScheduleMarker) {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	config, _ := json.Marshal(s)
	lines = append(lines, indexScheduleMarker+" "+string(config))
	if s.Enabled {
		lines = append(lines, fmt.Sprintf("%d %d * * * %s >/dev/null 2>&1 %s", minute, hour, indexRefreshCommand, indexScheduleMarker))
	}
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

func readIndexCrontab() ([]byte, error) {
	data, err := os.ReadFile(indexCrontabPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

// The root crontab is already persisted under /etc/kvm/cron by nanokvm-base.
// Cron uses the device timezone and runs independently of the web application.
func GetIndexSchedule() (IndexSchedule, error) {
	indexScheduleMu.Lock()
	defer indexScheduleMu.Unlock()
	data, err := readIndexCrontab()
	if err != nil {
		return IndexSchedule{}, err
	}
	return parseIndexSchedule(data)
}

func SetIndexSchedule(s IndexSchedule) error {
	indexScheduleMu.Lock()
	defer indexScheduleMu.Unlock()
	data, err := readIndexCrontab()
	if err != nil {
		return err
	}
	next, err := renderIndexSchedule(data, s)
	if err != nil {
		return err
	}
	if s.Enabled {
		if _, err := indexScheduleExec(nil, "rc-update", "add", "crond", "default"); err != nil {
			return fmt.Errorf("enable cron: %w", err)
		}
		if _, err := indexScheduleExec(nil, "rc-service", "crond", "status"); err != nil {
			if _, err := indexScheduleExec(nil, "rc-service", "crond", "start"); err != nil {
				return fmt.Errorf("start cron: %w", err)
			}
		}
	}
	// crontab notifies the running cron daemon and preserves the other jobs.
	output, err := indexScheduleExec(next, "crontab", "-")
	if err != nil {
		return fmt.Errorf("save index refresh schedule: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func IndexScheduleTimezone() string {
	if data, err := os.ReadFile("/etc/timezone"); err == nil && strings.TrimSpace(string(data)) != "" {
		return strings.TrimSpace(string(data))
	}
	if name, err := filepath.EvalSymlinks("/etc/localtime"); err == nil {
		if _, zone, ok := strings.Cut(name, "/zoneinfo/"); ok {
			return zone
		}
	}
	return "UTC"
}

// RunScheduledIndexRefresh never installs packages. It uses the same update
// lock, memory-aware APK runner and visible operation log as a manual refresh.
func RunScheduledIndexRefresh() error {
	lock, err := Lock()
	if err != nil {
		return err
	}
	defer lock.Close()
	schedule, err := GetIndexSchedule()
	if err != nil || !schedule.Enabled {
		return err
	}
	if err = saveSoftwareStatus(SoftwareStatus{State: "running", Action: "refresh"}); err != nil {
		return err
	}
	log, err := os.OpenFile(softwareLogPath(), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		_ = saveSoftwareStatus(SoftwareStatus{State: "failed", Action: "refresh", Message: err.Error()})
		return err
	}
	defer log.Close()
	// This entry point runs only inside the standalone helper.
	stdout, stderr := os.Stdout, os.Stderr
	defer func() { os.Stdout, os.Stderr = stdout, stderr }()
	os.Stdout, os.Stderr = log, log
	return RunSoftware("refresh", "")
}
