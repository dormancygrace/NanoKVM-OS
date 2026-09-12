package dashboard

import (
	"path/filepath"
	"strconv"
)

// cpuinfo_cur_freq is the driver hardware readback, not the selected target.
// Missing or invalid data stays unavailable rather than implying a clock.
func readCPUFrequency(sys string) *int {
	khz, err := strconv.Atoi(read(filepath.Join(sys, "devices/system/cpu/cpufreq/policy0/cpuinfo_cur_freq")))
	if err != nil || khz <= 0 {
		return nil
	}
	mhz := khz / 1000
	return &mhz
}
