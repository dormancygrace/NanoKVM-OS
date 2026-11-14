package redfish

import (
	"net/http"
	"strconv"
	"strings"
)

const (
	thermalPath = chassisPath + "/Thermal"
	powerPath   = chassisPath + "/Power"
	sensorsPath = chassisPath + "/Sensors"
)

// Reading is one reading of a host's sensor.
type Reading struct {
	// ID names the sensor across reports and restarts: monitoring keys its
	// history on it.
	ID   string
	Name string
	// Kind is the reading's kind as the agent reports it ("temperature",
	// "fan", ...), with the value in that kind's unit.
	Kind string
	// Context is the PhysicalContext of the part ("CPU"), "" when Redfish
	// has none for it.
	Context string
	Value   float64
}

// Sensors are the host's sensors. on is false while the feature is off (the
// add-on is not installed, or its transport is off): the resources do not
// exist then. fresh is false while the agent is silent: the resources
// exist, without readings, since an old reading must not pass for a
// current one.
type Sensors interface {
	Readings() (readings []Reading, fresh, on bool)
}

func (h *Handler) sensorsOn() bool {
	if h.Sensors == nil {
		return false
	}
	_, _, on := h.Sensors.Readings()
	return on
}

// sensorTypes maps the agent's kinds to the Sensor schema: ReadingType, the
// unit (UCUM) and the factor from the agent's unit. A kind that Redfish has
// no reading type for (data sizes, throughput, noise, factors) is not served.
var sensorTypes = map[string]struct {
	readingType, units string
	factor             float64
}{
	"temperature": {"Temperature", "Cel", 1},
	"voltage":     {"Voltage", "V", 1},
	"current":     {"Current", "A", 1},
	"power":       {"Power", "W", 1},
	"fan":         {"Rotational", "{rev}/min", 1},
	"control":     {"Percent", "%", 1},
	"load":        {"Percent", "%", 1},
	"level":       {"Percent", "%", 1},
	"humidity":    {"Humidity", "%", 1},
	"clock":       {"Frequency", "Hz", 1e6}, // MHz
	"frequency":   {"Frequency", "Hz", 1},
	"energy":      {"EnergyWh", "W.h", 1e-3},            // mWh
	"flow":        {"LiquidFlowLPM", "L/min", 1.0 / 60}, // L/h
}

func health(fresh bool) object {
	if !fresh {
		return object{"State": "UnavailableOffline"}
	}
	return object{"State": "Enabled", "Health": "OK"}
}

func (h *Handler) sensors(w http.ResponseWriter, r *http.Request, at string) {
	readings, fresh, _ := h.Sensors.Readings()
	// member is an entry of the arrays of Thermal and Power. Redfish names
	// it by its place in the array (MemberId is the last segment of its
	// JSON pointer); the sensor's lasting ID is that of its resource in
	// Sensors, which RelatedItem links to.
	member := func(parent, array string, index int, reading Reading, value string) object {
		m := object{
			"@odata.id": parent + "#/" + array + "/" + strconv.Itoa(index),
			"MemberId":  strconv.Itoa(index),
			"Name":      reading.Name,
			value:       reading.Value,
			"Status":    health(true),
		}
		if reading.Context != "" {
			m["PhysicalContext"] = reading.Context
		}
		if _, ok := sensorTypes[reading.Kind]; ok {
			m["RelatedItem"] = []object{link(sensorsPath + "/" + reading.ID)}
		}
		return m
	}
	switch at {
	case thermalPath:
		temperatures, fans := []object{}, []object{}
		for _, reading := range readings {
			switch reading.Kind {
			case "temperature":
				temperatures = append(temperatures, member(thermalPath, "Temperatures", len(temperatures), reading, "ReadingCelsius"))
			case "fan":
				fan := member(thermalPath, "Fans", len(fans), reading, "Reading")
				fan["ReadingUnits"] = "RPM"
				fans = append(fans, fan)
			}
		}
		h.get(w, r, func() any {
			return object{
				"@odata.id": thermalPath, "@odata.type": "#Thermal.v1_7_1.Thermal", "Id": "Thermal", "Name": "Host temperatures and fans",
				"Status": health(fresh), "Temperatures": temperatures, "Fans": fans,
			}
		})
	case powerPath:
		voltages := []object{}
		for _, reading := range readings {
			if reading.Kind == "voltage" {
				voltages = append(voltages, member(powerPath, "Voltages", len(voltages), reading, "ReadingVolts"))
			}
		}
		// No PowerControl: the agent reads the power of single parts, not
		// what the computer draws.
		h.get(w, r, func() any {
			return object{
				"@odata.id": powerPath, "@odata.type": "#Power.v1_7_1.Power", "Id": "Power", "Name": "Host voltages",
				"Voltages": voltages,
			}
		})
	case sensorsPath:
		members := []string{}
		for _, reading := range readings {
			if _, ok := sensorTypes[reading.Kind]; ok {
				members = append(members, sensorsPath+"/"+reading.ID)
			}
		}
		h.get(w, r, func() any {
			return collection(sensorsPath, "#SensorCollection.SensorCollection", "Host sensors", members...)
		})
	default:
		id := strings.TrimPrefix(at, sensorsPath+"/")
		for _, reading := range readings {
			kind, ok := sensorTypes[reading.Kind]
			if !ok || reading.ID != id {
				continue
			}
			h.get(w, r, func() any {
				sensor := object{
					"@odata.id": at, "@odata.type": "#Sensor.v1_7_0.Sensor", "Id": id, "Name": reading.Name,
					"ReadingType": kind.readingType, "ReadingUnits": kind.units, "Reading": reading.Value * kind.factor,
					"Status": health(true),
				}
				if reading.Context != "" {
					sensor["PhysicalContext"] = reading.Context
				}
				return sensor
			})
			return
		}
		writeError(w, http.StatusNotFound, "ResourceMissingAtURI", "The resource at the URI "+r.URL.Path+" was not found.")
	}
}
