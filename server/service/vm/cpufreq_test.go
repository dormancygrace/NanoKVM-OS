package vm

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func cpuFixture(t *testing.T) {
	t.Helper()
	oldRuntime := cpuFreqRuntimePreference
	cpuFreqRuntimePreference = filepath.Join(t.TempDir(), "runtime-cpufreq")
	oldPolicy, oldPreference, oldWrite, oldThermal := cpuFreqPolicy, cpuFreqPreference, cpuFreqWrite, cpuThermalRoot
	cpuThermalRoot = t.TempDir()
	cpuFreqPolicy = t.TempDir()
	cpuFreqPreference = filepath.Join(t.TempDir(), "cpufreq")
	t.Cleanup(func() {
		cpuFreqRuntimePreference = oldRuntime
		cpuFreqPolicy, cpuFreqPreference, cpuFreqWrite, cpuThermalRoot = oldPolicy, oldPreference, oldWrite, oldThermal
	})
	for name, value := range map[string]string{"scaling_driver": "sg2002-cpufreq", "cpuinfo_cur_freq": "850000"} {
		if err := os.WriteFile(filepath.Join(cpuFreqPolicy, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cpuFreqWrite = func(path string, data []byte, mode os.FileMode) error {
		if filepath.Base(path) == "scaling_setspeed" {
			return os.WriteFile(filepath.Join(cpuFreqPolicy, "cpuinfo_cur_freq"), data, mode)
		}
		return os.WriteFile(path, data, mode)
	}
}
func TestCPUFrequencyRequiresDriverAndActualReadback(t *testing.T) {
	cpuFixture(t)
	if err := applyCPUFreq(1000, true); err != nil {
		t.Fatal(err)
	}
	state := cpuFrequencyState()
	if state.Running != 1000 || state.Target != 1000 {
		t.Fatalf("state=%+v", state)
	}
	// A successful sysfs write is insufficient when readback did not change.
	cpuFreqWrite = os.WriteFile
	if err := applyCPUFreq(850, true); err == nil {
		t.Fatal("accepted false readback")
	}
	if state = cpuFrequencyState(); state.Target != 1000 {
		t.Fatal("saved unverified value")
	}
	os.Remove(filepath.Join(cpuFreqPolicy, "scaling_driver"))
	if err := applyCPUFreq(850, true); err == nil {
		t.Fatal("accepted missing driver")
	}
}
func TestCPUFrequencyErrorsDoNotPersistTarget(t *testing.T) {
	cpuFixture(t)
	if err := applyCPUFreq(1200, true); err == nil {
		t.Fatal("accepted unsupported clock")
	}
	cpuFreqWrite = func(string, []byte, os.FileMode) error { return errors.New("sysfs denied") }
	if err := applyCPUFreq(1000, true); err == nil {
		t.Fatal("ignored sysfs error")
	}
	if _, err := os.Stat(cpuFreqPreference); !os.IsNotExist(err) {
		t.Fatal("persisted failed target")
	}
}

func TestCPUFrequencyWaitsForEffectiveLimit(t *testing.T) {
	cpuFixture(t)
	baseWrite := cpuFreqWrite
	done := make(chan struct{})
	cpuFreqWrite = func(path string, data []byte, mode os.FileMode) error {
		if filepath.Base(path) == "scaling_max_freq" && string(data) == "1000000" {
			if err := os.WriteFile(path, []byte("850000"), mode); err != nil {
				return err
			}
			go func() { time.Sleep(40 * time.Millisecond); _ = os.WriteFile(path, data, mode); close(done) }()
			return nil
		}
		if filepath.Base(path) == "scaling_setspeed" {
			select {
			case <-done:
			default:
				return errors.New("setspeed before effective limit")
			}
		}
		return baseWrite(path, data, mode)
	}
	// Runtime 850 raises the allowed ceiling temporarily then lowers it again.
	if err := applyCPUFreq(850, false); err != nil {
		t.Fatal(err)
	}
}

func TestCPUFrequencyPreservesTargetWhileThermallyClamped(t *testing.T) {
	cpuFixture(t)
	zone := filepath.Join(cpuThermalRoot, "thermal_zone0")
	cooling := filepath.Join(zone, "cdev0")
	if err := os.MkdirAll(cooling, 0700); err != nil {
		t.Fatal(err)
	}
	for path, value := range map[string]string{
		filepath.Join(zone, "type"):                      "sg2002-cpu",
		filepath.Join(cooling, "type"):                   "cpufreq-cpu0",
		filepath.Join(cooling, "cur_state"):              "1",
		filepath.Join(cpuFreqPolicy, "cpuinfo_min_freq"): "850000",
		filepath.Join(cpuFreqPolicy, "cpuinfo_cur_freq"): "850000",
	} {
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	baseWrite := cpuFreqWrite
	cpuFreqWrite = func(path string, data []byte, mode os.FileMode) error {
		if filepath.Base(path) == "scaling_max_freq" {
			return os.WriteFile(path, []byte("850000"), mode)
		}
		if filepath.Base(path) == "scaling_setspeed" {
			return os.WriteFile(path, data, mode)
		}
		return baseWrite(path, data, mode)
	}
	for _, target := range []int{1000, 850, 1000} {
		if err := applyCPUFreq(target, true); err != nil {
			t.Fatal(err)
		}
		state := cpuFrequencyState()
		if !state.Throttled || state.Running != 850 || state.Target != target {
			t.Fatalf("state=%+v", state)
		}
	}
	// Once the thermal core releases its cap, a normal request must restore
	// the selected frequency; a false low hardware readback still fails.
	if err := os.WriteFile(filepath.Join(cooling, "cur_state"), []byte("0"), 0600); err != nil {
		t.Fatal(err)
	}
	cpuFreqWrite = baseWrite
	if err := applyCPUFreq(1000, false); err != nil {
		t.Fatal(err)
	}
	state := cpuFrequencyState()
	if state.Throttled || state.Running != 1000 || state.Target != 1000 {
		t.Fatalf("state=%+v", state)
	}
}

func TestCPUFrequencyOverclockIsBootScopedAndCapabilityChecked(t *testing.T) {
	cpuFixture(t)
	if cpuFrequencyState().Target != 1000 {
		t.Fatal("default must be 1000 MHz")
	}
	if err := applyCPUFreq(1100, true); err == nil {
		t.Fatal("old kernel accepted overclock")
	}
	os.WriteFile(filepath.Join(cpuFreqPolicy, "scaling_available_frequencies"), []byte("600000 850000 1000000 1050000 1075000 1100000 1125000 1150000"), 0600)
	for _, target := range []int{1050, 1075, 1100, 1125, 1150} {
		if err := applyCPUFreq(target, true); err != nil {
			t.Fatal(err)
		}
		if s := cpuFrequencyState(); s.Target != target || s.Running != target {
			t.Fatalf("state=%+v", s)
		}
		boot, _ := os.ReadFile(cpuFreqPreference)
		if string(boot) != "1000\n" {
			t.Fatalf("unsafe boot target %s", boot)
		}
	}
	os.Remove(cpuFreqRuntimePreference)
	ApplySavedCPUFrequency()
	if s := cpuFrequencyState(); s.Target != 1000 || s.Running != 1000 {
		t.Fatalf("boot state=%+v", s)
	}
}

func TestCPUFrequencyExpandedCoolingTable(t *testing.T) {
	cpuFixture(t)
	zone := filepath.Join(cpuThermalRoot, "thermal_zone0")
	cooling := filepath.Join(zone, "cdev0")
	os.MkdirAll(cooling, 0700)
	for path, value := range map[string]string{
		filepath.Join(zone, "type"):                                   "sg2002-cpu",
		filepath.Join(cooling, "type"):                                "cpufreq-cpu0",
		filepath.Join(cooling, "cur_state"):                           "6",
		filepath.Join(cpuFreqPolicy, "scaling_available_frequencies"): "600000 850000 1000000 1050000 1075000 1100000 1125000 1150000",
	} {
		os.WriteFile(path, []byte(value), 0600)
	}
	cpuFreqWrite = func(path string, data []byte, mode os.FileMode) error {
		if filepath.Base(path) == "scaling_setspeed" {
			return nil
		}
		if filepath.Base(path) == "scaling_max_freq" {
			data = []byte("850000")
		}
		return os.WriteFile(path, data, mode)
	}
	if err := applyCPUFreq(1125, true); err != nil {
		t.Fatal(err)
	}
	if s := cpuFrequencyState(); !s.Throttled || s.Target != 1125 || s.Running != 850 {
		t.Fatalf("state=%+v", s)
	}
}
