// Package dashboard collects read-only Linux telemetry without subprocesses.
package dashboard

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type CPU struct {
	Total uint64 `json:"total"`
	Idle  uint64 `json:"idle"`
}
type Storage struct {
	Path      string `json:"path"`
	Available bool   `json:"available"`
	Total     uint64 `json:"total"`
	Free      uint64 `json:"free"`
	Used      uint64 `json:"used"`
	ReadOnly  bool   `json:"readOnly"`
}
type Interface struct {
	Kind      string   `json:"kind"`
	Name      string   `json:"name"`
	MAC       string   `json:"mac"`
	MTU       int      `json:"mtu"`
	Up        bool     `json:"up"`
	Connected bool     `json:"connected"`
	Wireless  bool     `json:"wireless"`
	Addresses []string `json:"addresses"`
	Received  *uint64  `json:"received"`
	Sent      *uint64  `json:"sent"`
}
type Status struct {
	Now          int64       `json:"now"`
	Hostname     string      `json:"hostname"`
	Kernel       string      `json:"kernel"`
	Architecture string      `json:"architecture"`
	Cores        int         `json:"cores"`
	Uptime       *float64    `json:"uptime"`
	CPU          *CPU        `json:"cpu"`
	Load         []string    `json:"load"`
	CPUFrequency *int        `json:"cpuFrequency"`
	Temperature  *float64    `json:"temperature"`
	Storage      []Storage   `json:"storage"`
	Interfaces   []Interface `json:"interfaces"`
}

func read(path string) string { b, _ := os.ReadFile(path); return strings.TrimSpace(string(b)) }
func counter(path string) *uint64 {
	n, err := strconv.ParseUint(read(path), 10, 64)
	if err != nil {
		return nil
	}
	return &n
}

// Guest time is already included in user/nice; sum only the first eight fields.
func parseCPU(text string) *CPU {
	fields := strings.Fields(strings.SplitN(text, "\n", 2)[0])
	if len(fields) < 5 || fields[0] != "cpu" {
		return nil
	}
	var result CPU
	for i, field := range fields[1:min(len(fields), 9)] {
		n, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return nil
		}
		result.Total += n
		if i == 3 || i == 4 {
			result.Idle += n
		}
	}
	return &result
}
func mountedPaths(text string) map[string]bool {
	result := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) > 1 {
			result[f[1]] = true
		}
	}
	return result
}
func storage(path string, mounted bool) Storage {
	result := Storage{Path: path}
	if !mounted {
		return result
	}
	var stat unix.Statfs_t
	if unix.Statfs(path, &stat) != nil || stat.Blocks == 0 {
		return result
	}
	result.Available = true
	result.Total = stat.Blocks * uint64(stat.Bsize)
	result.Free = stat.Bavail * uint64(stat.Bsize)
	result.Used = (stat.Blocks - min(stat.Blocks, stat.Bfree)) * uint64(stat.Bsize)
	result.ReadOnly = stat.Flags&unix.ST_RDONLY != 0
	return result
}
func Read() Status {
	result := Status{Now: time.Now().UnixMilli(), Hostname: read("/proc/sys/kernel/hostname"), Kernel: read("/proc/sys/kernel/osrelease"), Architecture: runtime.GOARCH, Cores: runtime.NumCPU(), CPU: parseCPU(read("/proc/stat")), Interfaces: []Interface{}}
	if fields := strings.Fields(read("/proc/uptime")); len(fields) > 0 {
		if n, err := strconv.ParseFloat(fields[0], 64); err == nil && n >= 0 {
			result.Uptime = &n
		}
	}
	if fields := strings.Fields(read("/proc/loadavg")); len(fields) >= 3 {
		result.Load = fields[:3]
	}
	result.Temperature = readTemperature("/sys")
	result.CPUFrequency = readCPUFrequency("/sys")
	mounts := mountedPaths(read("/proc/mounts"))
	for _, path := range []string{"/", "/boot", "/data"} {
		result.Storage = append(result.Storage, storage(path, mounts[path]))
	}
	kinds := linkKinds()
	interfaces, _ := net.Interfaces()
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		base := filepath.Join("/sys/class/net", iface.Name)
		_, wirelessErr := os.Stat(filepath.Join(base, "wireless"))
		item := Interface{Kind: kinds[iface.Index], Name: iface.Name, MAC: iface.HardwareAddr.String(), MTU: iface.MTU, Up: iface.Flags&net.FlagUp != 0, Connected: iface.Flags&net.FlagRunning != 0, Wireless: wirelessErr == nil || strings.HasPrefix(iface.Name, "wl"), Addresses: []string{}, Received: counter(filepath.Join(base, "statistics/rx_bytes")), Sent: counter(filepath.Join(base, "statistics/tx_bytes"))}
		if carrier := read(filepath.Join(base, "carrier")); carrier == "0" {
			item.Connected = false
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			item.Addresses = append(item.Addresses, fmt.Sprint(addr))
		}
		result.Interfaces = append(result.Interfaces, item)
	}
	return result
}
