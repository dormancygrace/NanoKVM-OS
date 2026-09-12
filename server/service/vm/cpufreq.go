package vm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"NanoKVM-Server/proto"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

var cpuFreqMu sync.Mutex
var cpuFreqPolicy = "/sys/devices/system/cpu/cpufreq/policy0"
var cpuFreqPreference = "/etc/kvm/cpufreq"
var cpuFreqRuntimePreference = "/run/nanokvm-cpufreq"
var cpuFreqWrite = os.WriteFile
var cpuThermalRoot = "/sys/class/thermal"

type cpuFrequencyStatus struct {
	Supported bool  `json:"supported"`
	Throttled bool  `json:"throttled"`
	Running   int   `json:"running"`
	Target    int   `json:"target"`
	Options   []int `json:"options"`
}

func readCPUFreq(name string) (string, error) {
	data, err := os.ReadFile(filepath.Join(cpuFreqPolicy, name))
	return strings.TrimSpace(string(data)), err
}

// Only accept the cap from this board's thermal zone and its CPU cooling
// device. A low clock or a user-written scaling_max_freq alone is not proof
// that thermal protection is active.
func cpuThermalLimited() bool {
	zones, _ := filepath.Glob(filepath.Join(cpuThermalRoot, "thermal_zone*"))
	for _, zone := range zones {
		kind, err := os.ReadFile(filepath.Join(zone, "type"))
		if err != nil || strings.TrimSpace(string(kind)) != "sg2002-cpu" {
			continue
		}
		devices, _ := filepath.Glob(filepath.Join(zone, "cdev[0-9]*"))
		for _, device := range devices {
			kind, err := os.ReadFile(filepath.Join(device, "type"))
			if err != nil || strings.TrimSpace(string(kind)) != "cpufreq-cpu0" {
				continue
			}
			state, err := os.ReadFile(filepath.Join(device, "cur_state"))
			if n, parseErr := strconv.Atoi(strings.TrimSpace(string(state))); err == nil && parseErr == nil && n > 0 {
				return true
			}
		}
	}
	return false
}

func cpuFrequencyState() cpuFrequencyStatus {
	state := cpuFrequencyStatus{Target: 1000, Options: []int{}}
	if data, err := os.ReadFile(cpuFreqPreference); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && (n == 850 || n == 1000) {
			state.Target = n
		}
	}
	if data, err := os.ReadFile(cpuFreqRuntimePreference); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && validCPUFrequency(n) {
			state.Target = n
		}
	}
	driver, err := readCPUFreq("scaling_driver")
	if err != nil || driver != "sg2002-cpufreq" {
		return state
	}
	text, err := readCPUFreq("cpuinfo_cur_freq")
	if err != nil {
		return state
	}
	khz, err := strconv.Atoi(text)
	if err != nil || khz <= 0 {
		return state
	}
	state.Running = khz / 1000
	state.Throttled = cpuThermalLimited()
	state.Supported = true
	state.Options = cpuFrequencyOptions()
	return state
}

func validCPUFrequency(mhz int) bool {
	switch mhz {
	case 850, 1000, 1050, 1075, 1100, 1125, 1150:
		return true
	}
	return false
}

func cpuFrequencyOptions() []int {
	available, err := readCPUFreq("scaling_available_frequencies")
	if err != nil {
		return []int{850, 1000}
	}
	var options []int
	for _, value := range strings.Fields(available) {
		khz, err := strconv.Atoi(value)
		if err == nil && khz%1000 == 0 && validCPUFrequency(khz/1000) {
			options = append(options, khz/1000)
		}
	}
	return options
}

func setCPUFreqRuntime(mhz int) error {
	supported := false
	for _, option := range cpuFrequencyOptions() {
		if option == mhz {
			supported = true
		}
	}
	if !supported {
		return errors.New("unsupported CPU frequency")
	}
	// A fixed userspace target makes the selected value unambiguous. Keep the
	// minimum valid while lowering the maximum, then verify hardware readback.
	minimum := "850000"
	if value, err := readCPUFreq("cpuinfo_min_freq"); err == nil && value == "600000" {
		minimum = value
	}
	ceiling := max(1000, mhz)
	settings := [][2]string{{"scaling_governor", "userspace"}, {"scaling_min_freq", minimum}, {"scaling_max_freq", strconv.Itoa(ceiling * 1000)}, {"scaling_setspeed", strconv.Itoa(mhz * 1000)}, {"scaling_max_freq", strconv.Itoa(mhz * 1000)}}
	for _, s := range settings {
		if err := cpuFreqWrite(filepath.Join(cpuFreqPolicy, s[0]), []byte(s[1]), 0644); err != nil {
			return fmt.Errorf("%s: %w", s[0], err)
		}
		// QoS limit writes queue policy work. Wait for the effective limit
		// before setspeed, otherwise userspace silently clamps to the old max.
		if s[0] == "scaling_min_freq" || s[0] == "scaling_max_freq" {
			deadline := time.Now().Add(2 * time.Second)
			for {
				actual, err := readCPUFreq(s[0])
				if err != nil {
					return err
				}
				if actual == s[1] || (s[0] == "scaling_max_freq" && actual == "850000" && cpuThermalLimited()) {
					break
				}
				if time.Now().After(deadline) {
					return fmt.Errorf("%s did not apply: %s", s[0], actual)
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
	}
	state := cpuFrequencyState()
	if !state.Supported || (state.Running != mhz && !(state.Throttled && state.Running == 850)) {
		return fmt.Errorf("CPU clock readback is %d MHz, requested %d", state.Running, mhz)
	}
	return nil
}

func persistCPUFreq(mhz int) error {
	// Overclock survives application restarts, but /run is cleared at boot.
	if err := persistCPUFreqFile(cpuFreqPreference, min(mhz, 1000)); err != nil {
		return err
	}
	return persistCPUFreqFile(cpuFreqRuntimePreference, mhz)
}

func persistCPUFreqFile(path string, mhz int) error {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, ".cpufreq-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.WriteString(strconv.Itoa(mhz) + "\n"); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func applyCPUFreq(mhz int, persist bool) error {
	cpuFreqMu.Lock()
	defer cpuFreqMu.Unlock()
	before := cpuFrequencyState()
	if !before.Supported {
		return errors.New("qualified CPU frequency driver is unavailable")
	}
	err := setCPUFreqRuntime(mhz)
	if err == nil && persist {
		err = persistCPUFreq(mhz)
	}
	restoreTarget := before.Running
	if before.Throttled {
		restoreTarget = before.Target
	}
	if err != nil && validCPUFrequency(restoreTarget) {
		if restore := setCPUFreqRuntime(restoreTarget); restore != nil {
			err = errors.Join(err, fmt.Errorf("restore CPU clock: %w", restore))
		}
	}
	return err
}

// ApplySavedCPUFrequency runs only after the matching kernel has exposed a
// qualified driver. A missing driver leaves stock hardware configuration alone.
func ApplySavedCPUFrequency() {
	state := cpuFrequencyState()
	if state.Supported {
		if err := applyCPUFreq(state.Target, false); err != nil {
			log.Errorf("apply saved CPU frequency: %v", err)
		}
	}
}

func (s *Service) GetCPUFrequency(c *gin.Context) {
	var rsp proto.Response
	cpuFreqMu.Lock()
	state := cpuFrequencyState()
	cpuFreqMu.Unlock()
	rsp.OkRspWithData(c, state)
}

func (s *Service) SetCPUFrequency(c *gin.Context) {
	var req struct {
		Target int `json:"target" validate:"oneof=850 1000 1050 1075 1100 1125 1150"`
	}
	var rsp proto.Response
	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid CPU frequency")
		return
	}
	if err := applyCPUFreq(req.Target, true); err != nil {
		rsp.ErrRsp(c, -2, err.Error())
		return
	}
	rsp.OkRsp(c)
}
