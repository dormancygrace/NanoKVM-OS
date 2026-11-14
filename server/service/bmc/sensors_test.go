package bmc

import (
	"testing"

	"NanoKVM-Server/hostsensors"
)

func TestRedfishSensorsAreTheFreshReport(t *testing.T) {
	report := &hostsensors.Report{Sensors: []hostsensors.Sensor{
		{Hardware: "Ryzen", Type: "cpu", Name: "Package", Kind: "temperature", Value: 54.5},
		{Hardware: "Ryzen", Type: "cpu", Name: "Package", Kind: "power", Value: 65},
		{Hardware: "Ryzen", Type: "cpu", Name: "Package", Kind: "temperature", Value: 1}, // named twice
		{Hardware: "Box", Type: "other", Name: "Load", Kind: "load", Value: 3},
	}}
	status, on := hostsensors.Status{Report: report}, true
	sensors := RedfishSensors(func() (hostsensors.Status, bool) { return status, on })
	readings, fresh, _ := sensors.Readings()
	if !fresh || len(readings) != 3 || readings[0].Name != "Ryzen Package" || readings[0].Context != "CPU" || readings[2].Context != "" ||
		readings[0].ID == readings[1].ID || len(readings[0].ID) != 12 {
		t.Fatalf("readings: %+v fresh %v", readings, fresh)
	}
	again, _, _ := sensors.Readings()
	if again[0].ID != readings[0].ID {
		t.Errorf("the ID of a reading changed between reports: %s, %s", readings[0].ID, again[0].ID)
	}
	status.Stale = true
	if readings, fresh, on := sensors.Readings(); len(readings) != 0 || fresh || !on {
		t.Errorf("a stale report: %d readings, fresh %v, on %v", len(readings), fresh, on)
	}
	on = false
	if _, _, on := sensors.Readings(); on {
		t.Error("sensors that are off are on")
	}
}
