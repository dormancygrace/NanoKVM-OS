package bmc

import (
	"crypto/sha256"
	"encoding/hex"

	"NanoKVM-Server/hostsensors"
	"NanoKVM-Server/internal/bmc/redfish"
)

// contexts are Redfish's PhysicalContext values for the agent's part
// classes; "other" has none.
var contexts = map[string]string{
	"cpu": "CPU", "gpu": "GPU", "motherboard": "SystemBoard", "memory": "Memory", "storage": "StorageDevice",
	"network": "NetworkingDevice", "cooler": "Fan", "psu": "PowerSupply", "battery": "Battery",
}

// redfishSensors are the readings of the computer sensors add-on's agent
// as Redfish's sensors. All of them: the page's choice of what it shows is
// the page's.
type redfishSensors struct {
	// status is the last report, and whether the feature is on.
	status func() (hostsensors.Status, bool)
}

// RedfishSensors serves what status returns.
func RedfishSensors(status func() (hostsensors.Status, bool)) redfish.Sensors {
	return redfishSensors{status}
}

func (s redfishSensors) Readings() (readings []redfish.Reading, fresh, on bool) {
	status, on := s.status()
	if !on || status.Report == nil || status.Stale {
		return nil, false, on
	}
	seen := map[string]bool{}
	for _, sensor := range status.Report.Sensors {
		// The ID is made of what names the reading (hostsensors.Key), so it
		// is the same in every report and after a restart.
		sum := sha256.Sum256([]byte(hostsensors.Key(sensor)))
		id := hex.EncodeToString(sum[:6])
		if seen[id] {
			continue // the agent names a reading once; a second is a duplicate
		}
		seen[id] = true
		readings = append(readings, redfish.Reading{
			ID: id, Name: sensor.Hardware + " " + sensor.Name, Kind: sensor.Kind, Context: contexts[sensor.Type], Value: sensor.Value,
		})
	}
	return readings, true, true
}
