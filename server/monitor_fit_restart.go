package main

import (
	"os"
	"strconv"
	"syscall"
	"time"

	"NanoKVM-Server/common"

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
// The vendor media libraries open their devices without O_CLOEXEC, so their
// descriptors would survive exec and the drivers would keep this process's
// VPSS group, VI pipe and video buffers: the new process then builds a second
// set next to them (9.4 MiB more at 1920x1080, enough to exhaust a 40 MiB
// pool). Release the pipeline as on SIGTERM and close every other descriptor
// on exec, so the restart starts from the state of a fresh start.
func restartAfterMonitorFit() {
	if os.Getenv(monitorFitMarker) != "" {
		return
	}
	time.Sleep(monitorFitSettle)
	logrus.Info("restarting to re-detect the HDMI input after lowering the monitor profile")
	common.GetKvmVision().Close()
	closeOnExec()
	err := execSelf(append(os.Environ(), monitorFitMarker+"=1"))
	logrus.Errorf("restart after lowering the monitor profile failed: %v", err)
}

// closeOnExec marks every descriptor above stderr close-on-exec.
func closeOnExec() {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		logrus.Warnf("cannot list descriptors before restart: %v", err)
		return
	}
	for _, entry := range entries {
		if fd, err := strconv.Atoi(entry.Name()); err == nil && fd > 2 {
			syscall.CloseOnExec(fd)
		}
	}
}
