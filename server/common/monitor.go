package common

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var monitorMutex sync.Mutex

// ApplyMonitorResolution changes the virtual HDMI monitor, without forcing the
// HDMI source to adopt it. BIOS and operating systems may choose other timings.
func ApplyMonitorResolution(height uint16) error {
	monitorMutex.Lock()
	defer monitorMutex.Unlock()
	_, ok := ResolutionMap[height]
	if !ok {
		return fmt.Errorf("unsupported monitor resolution")
	}
	product, err := os.ReadFile("/etc/kvm/hw")
	if err != nil || strings.TrimSpace(string(product)) != "pcie" {
		return fmt.Errorf("monitor resolution switching requires NanoKVM PCIe")
	}
	chip, err := os.ReadFile("/etc/kvm/hdmi_version")
	if err != nil || strings.TrimSpace(string(chip)) != "ux" {
		return fmt.Errorf("monitor resolution switching currently requires LT6911UXC")
	}
	profile := fmt.Sprintf("NanoKVM-monitor-%d.bin", height)
	if height == 0 {
		profile = "NanoKVM-stock.bin"
		if SupportsQHD() {
			profile = "NanoKVM-QHD30.bin"
		}
	}
	path := filepath.Join("/usr/share/nanokvm/edid", profile)
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("monitor profile is unavailable: %w", err)
	}
	if err := GetKvmVision().ApplyMonitorProfile(path); err != nil {
		return err
	}
	if err := os.WriteFile("/etc/kvm/monitor_resolution", []byte(fmt.Sprint(height)), 0600); err != nil {
		return fmt.Errorf("monitor changed, but saving its setting failed: %w", err)
	}
	return nil
}
