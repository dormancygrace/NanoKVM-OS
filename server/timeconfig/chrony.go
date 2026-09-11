package timeconfig

import (
	"context"
	"encoding/csv"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func ChronySynchronized() (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/chronyc", "-n", "-c", "-u", "root", "-h", "/run/chrony/chronyd.sock", "tracking")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return false, err
	}
	return parseChronyTracking(string(out))
}

// chronyc 4.9 tracking CSV has 14 fields; the final field is the leap status.
func parseChronyTracking(text string) (bool, error) {
	rows, err := csv.NewReader(strings.NewReader(strings.TrimSpace(text))).ReadAll()
	if err != nil || len(rows) != 1 || len(rows[0]) != 14 {
		return false, errors.New("invalid chrony tracking response")
	}
	fields := rows[0]
	stratum, err := strconv.Atoi(fields[2])
	if err != nil {
		return false, err
	}
	// Never report a free-running local reference as external synchronization.
	if stratum < 1 || stratum > 15 || fields[0] == "00000000" || fields[0] == "7F7F0101" {
		return false, nil
	}
	switch fields[13] {
	case "Normal", "Insert second", "Delete second":
		return true, nil
	case "Not synchronised":
		return false, nil
	default:
		return false, errors.New("unknown chrony leap status")
	}
}
