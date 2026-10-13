package main

import (
	"os"
	"time"

	"github.com/sirupsen/logrus"
)

// The capture detects the HDMI input when it starts and keeps an input that
// the video memory of this boot cannot hold (3840x2160 on a QHD pool, seen
// right after the saved monitor profile was lowered) until the next start:
// neither a later mode change of the computer nor an HDMI reset recovers it,
// a restart does. So after the profile was lowered, restart once when the
// computer has had time to switch modes.
const (
	monitorFitSettle = 20 * time.Second
	monitorFitMarker = "NKOS_MONITOR_FIT_RESTARTED"
)

// restartAfterMonitorFit replaces the process in place, once per start chain.
func restartAfterMonitorFit() {
	if os.Getenv(monitorFitMarker) != "" {
		return
	}
	time.Sleep(monitorFitSettle)
	logrus.Info("restarting to re-detect the HDMI input after lowering the monitor profile")
	err := execSelf(append(os.Environ(), monitorFitMarker+"=1"))
	logrus.Errorf("restart after lowering the monitor profile failed: %v", err)
}
