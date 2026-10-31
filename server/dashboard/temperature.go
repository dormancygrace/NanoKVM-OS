package dashboard

import (
	"path/filepath"
	"strconv"
	"strings"
)

// Prefer the board sensor over unrelated USB or storage hwmon devices. Errors
// and readings before the first hardware conversion are unavailable, never 0 C.
func readTemperature(sys string) *float64 {
	var samples []string
	hwmons, _ := filepath.Glob(filepath.Join(sys, "class/hwmon/hwmon*"))
	for _, dir := range hwmons {
		// nanokvm; sg2002 is the kernel from before 3.7.0 until the reboot.
		if name := read(filepath.Join(dir, "name")); name != "nanokvm" && name != "sg2002" {
			continue
		}
		paths, _ := filepath.Glob(filepath.Join(dir, "temp*_input"))
		samples = append(samples, paths...)
	}
	if len(samples) == 0 {
		zones, _ := filepath.Glob(filepath.Join(sys, "class/thermal/thermal_zone*"))
		for _, dir := range zones {
			name := strings.ToLower(read(filepath.Join(dir, "type")))
			if strings.Contains(name, "soc") || strings.Contains(name, "cpu") || strings.Contains(name, "nanokvm") || strings.Contains(name, "sg2002") {
				samples = append(samples, filepath.Join(dir, "temp"))
			}
		}
	}
	var result *float64
	for _, path := range samples {
		n, err := strconv.ParseInt(read(path), 10, 64)
		if err != nil || n < -40000 || n > 150000 {
			continue
		}
		temperature := float64(n) / 1000
		if result == nil || temperature > *result {
			result = &temperature
		}
	}
	return result
}
