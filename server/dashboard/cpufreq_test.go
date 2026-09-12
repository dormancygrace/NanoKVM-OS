package dashboard

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCPUFrequencyUsesActualClock(t *testing.T) {
	sys := t.TempDir()
	policy := filepath.Join(sys, "devices/system/cpu/cpufreq/policy0")
	os.MkdirAll(policy, 0700)
	os.WriteFile(filepath.Join(policy, "scaling_setspeed"), []byte("1150000"), 0600)
	if readCPUFrequency(sys) != nil {
		t.Fatal("target must not replace missing hardware readback")
	}
	for _, value := range []string{"850000", "1000000", "1125000", "0", "invalid"} {
		os.WriteFile(filepath.Join(policy, "cpuinfo_cur_freq"), []byte(value), 0600)
		actual := readCPUFrequency(sys)
		if value == "0" || value == "invalid" {
			if actual != nil {
				t.Fatal("accepted invalid readback")
			}
			continue
		}
		expected := map[string]int{"850000": 850, "1000000": 1000, "1125000": 1125}[value]
		if actual == nil || *actual != expected {
			t.Fatalf("value=%s result=%v", value, actual)
		}
	}
}
