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
	oldFlag, oldPending, oldFailed, oldSettle := cpuFreqBootFlag, cpuFreqBootPending, cpuFreqBootFailed, cpuFreqBootSettle
	cpuThermalRoot = t.TempDir()
	cpuFreqPolicy = t.TempDir()
	etc := t.TempDir()
	cpuFreqPreference = filepath.Join(etc, "cpufreq")
	cpuFreqBootFlag = filepath.Join(etc, "cpufreq.boot")
	cpuFreqBootPending = filepath.Join(etc, "cpufreq.boot-pending")
	cpuFreqBootFailed = filepath.Join(etc, "cpufreq.boot-failed")
	cpuFreqBootSettle = time.Hour
	t.Cleanup(func() {
		cpuFreqRuntimePreference = oldRuntime
		cpuFreqPolicy, cpuFreqPreference, cpuFreqWrite, cpuThermalRoot = oldPolicy, oldPreference, oldWrite, oldThermal
		cpuFreqBootFlag, cpuFreqBootPending, cpuFreqBootFailed, cpuFreqBootSettle = oldFlag, oldPending, oldFailed, oldSettle
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
	if err := applyCPUFreq(1000, true, false); err != nil {
		t.Fatal(err)
	}
	state := cpuFrequencyState()
	if state.Running != 1000 || state.Target != 1000 {
		t.Fatalf("state=%+v", state)
	}
	// A successful sysfs write is insufficient when readback did not change.
	cpuFreqWrite = os.WriteFile
	if err := applyCPUFreq(850, true, false); err == nil {
		t.Fatal("accepted false readback")
	}
	if state = cpuFrequencyState(); state.Target != 1000 {
		t.Fatal("saved unverified value")
	}
	os.Remove(filepath.Join(cpuFreqPolicy, "scaling_driver"))
	if err := applyCPUFreq(850, true, false); err == nil {
		t.Fatal("accepted missing driver")
	}
}
func TestCPUFrequencyErrorsDoNotPersistTarget(t *testing.T) {
	cpuFixture(t)
	if err := applyCPUFreq(1200, true, false); err == nil {
		t.Fatal("accepted unsupported clock")
	}
	cpuFreqWrite = func(string, []byte, os.FileMode) error { return errors.New("sysfs denied") }
	if err := applyCPUFreq(1000, true, false); err == nil {
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
	if err := applyCPUFreq(850, false, false); err != nil {
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
		if err := applyCPUFreq(target, true, false); err != nil {
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
	if err := applyCPUFreq(1000, false, false); err != nil {
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
	if err := applyCPUFreq(1100, true, false); err == nil {
		t.Fatal("old kernel accepted overclock")
	}
	os.WriteFile(filepath.Join(cpuFreqPolicy, "scaling_available_frequencies"), []byte("600000 850000 1000000 1050000 1075000 1100000 1125000 1150000"), 0600)
	for _, target := range []int{1050, 1075, 1100, 1125, 1150} {
		if err := applyCPUFreq(target, true, false); err != nil {
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

func TestCPUFrequencyBootOverclockFallsBackAfterFrozenStart(t *testing.T) {
	cpuFixture(t)
	os.WriteFile(filepath.Join(cpuFreqPolicy, "scaling_available_frequencies"), []byte("600000 850000 1000000 1050000 1075000 1100000 1125000 1150000"), 0600)
	if err := applyCPUFreq(1100, true, true); err != nil {
		t.Fatal(err)
	}
	if s := cpuFrequencyState(); !s.ApplyAtBoot || s.Target != 1100 {
		t.Fatalf("state=%+v", s)
	}
	reboot := func() {
		t.Helper()
		os.Remove(cpuFreqRuntimePreference)
		os.WriteFile(filepath.Join(cpuFreqPolicy, "cpuinfo_cur_freq"), []byte("1000000"), 0600)
		ApplySavedCPUFrequency()
	}

	reboot()
	if s := cpuFrequencyState(); s.Running != 1100 || !fileExists(cpuFreqBootPending) {
		t.Fatalf("overclocked start: state=%+v pending=%v", s, fileExists(cpuFreqBootPending))
	}
	// An application restart in the same boot is not a new start.
	ApplySavedCPUFrequency()
	if s := cpuFrequencyState(); s.Running != 1100 || !s.ApplyAtBoot {
		t.Fatalf("restart: state=%+v", s)
	}

	// The device froze before the start settled: the marker is still there.
	reboot()
	s := cpuFrequencyState()
	if s.Running != 1000 || s.Target != 1000 || s.ApplyAtBoot || !s.BootFallback || fileExists(cpuFreqBootPending) {
		t.Fatalf("fallback: state=%+v pending=%v", s, fileExists(cpuFreqBootPending))
	}
	reboot()
	if s := cpuFrequencyState(); s.Running != 1000 {
		t.Fatalf("fallback must persist: state=%+v", s)
	}

	if err := applyCPUFreq(1000, true, false); err != nil {
		t.Fatal(err)
	}
	if cpuFrequencyState().BootFallback {
		t.Fatal("saving a choice must clear the failed-start report")
	}

	// A start that settles removes its marker.
	cpuFreqBootSettle = time.Millisecond
	if err := applyCPUFreq(1050, true, true); err != nil {
		t.Fatal(err)
	}
	reboot()
	deadline := time.Now().Add(2 * time.Second)
	for fileExists(cpuFreqBootPending) {
		if time.Now().After(deadline) {
			t.Fatal("settled start kept its marker")
		}
		time.Sleep(time.Millisecond)
	}
	reboot()
	if s := cpuFrequencyState(); s.Running != 1050 || !s.ApplyAtBoot {
		t.Fatalf("settled overclock: state=%+v", s)
	}
}

func TestCPUFrequencyBootOverclockSettlesAcrossRestarts(t *testing.T) {
	cpuFixture(t)
	os.WriteFile(filepath.Join(cpuFreqPolicy, "scaling_available_frequencies"), []byte("600000 850000 1000000 1050000 1075000 1100000 1125000 1150000"), 0600)
	if err := applyCPUFreq(1100, true, true); err != nil {
		t.Fatal(err)
	}
	reboot := func() {
		t.Helper()
		os.Remove(cpuFreqRuntimePreference)
		os.WriteFile(filepath.Join(cpuFreqPolicy, "cpuinfo_cur_freq"), []byte("1000000"), 0600)
		ApplySavedCPUFrequency()
	}
	reboot()
	if !fileExists(cpuFreqBootPending) {
		t.Fatal("overclocked start left no marker")
	}
	// The marking process was restarted shortly after boot; the boot itself
	// kept running past the settle period before the next restart.
	old := time.Now().Add(-2 * cpuFreqBootSettle)
	if err := os.Chtimes(cpuFreqBootPending, old, old); err != nil {
		t.Fatal(err)
	}
	ApplySavedCPUFrequency()
	deadline := time.Now().Add(2 * time.Second)
	for fileExists(cpuFreqBootPending) {
		if time.Now().After(deadline) {
			t.Fatal("a later start in a settled boot kept the marker")
		}
		time.Sleep(time.Millisecond)
	}
	reboot()
	if s := cpuFrequencyState(); s.Running != 1100 || !s.ApplyAtBoot || s.BootFallback {
		t.Fatalf("restart before settle was taken for a frozen boot: state=%+v", s)
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
	if err := applyCPUFreq(1125, true, false); err != nil {
		t.Fatal(err)
	}
	if s := cpuFrequencyState(); !s.Throttled || s.Target != 1125 || s.Running != 850 {
		t.Fatalf("state=%+v", s)
	}
}
