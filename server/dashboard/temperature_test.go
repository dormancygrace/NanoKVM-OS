package dashboard

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTemperatureSourceAndUnavailableReadings(t *testing.T) {
	root := t.TempDir()
	put := func(path, value string) {
		t.Helper()
		path = filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if got := readTemperature(root); got != nil {
		t.Fatalf("missing sensor: %v", *got)
	}
	put("class/hwmon/hwmon0/name", "nvme")
	put("class/hwmon/hwmon0/temp1_input", "99000")
	put("class/thermal/thermal_zone0/type", "soc-thermal")
	put("class/thermal/thermal_zone0/temp", "45000")
	if got := readTemperature(root); got == nil || *got != 45 {
		t.Fatalf("thermal fallback: %v", got)
	}
	put("class/hwmon/hwmon1/name", "sg2002")
	put("class/hwmon/hwmon1/temp1_input", "52875")
	if got := readTemperature(root); got == nil || *got != 52.875 {
		t.Fatalf("board sensor: %v", got)
	}
	for _, invalid := range []string{"", "NaN", "-273000", "151000", "not available"} {
		put("class/hwmon/hwmon1/temp1_input", invalid)
		if got := readTemperature(root); got != nil {
			t.Fatalf("invalid %q became %v", invalid, *got)
		}
	}
	put("class/hwmon/hwmon1/temp1_input", "0")
	if got := readTemperature(root); got == nil || *got != 0 {
		t.Fatal("legitimate 0 C must be preserved")
	}
}
