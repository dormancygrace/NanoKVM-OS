package bmc

// ATXPath is the server's loopback-only ATX API for the daemon. It takes the
// internal token, like the other /api/internal routes, and drives the same
// GPIO lines, pulse lock and LED monitor as the web page: the daemon never
// opens a GPIO line itself.
const ATXPath = "/api/internal/atx"

// ATXState is the answer of GET ATXPath.
type ATXState struct {
	// Which lines this board has: Lite has none, every ATX board has the
	// power and reset buttons and the power LED.
	PowerButton bool `json:"powerButton"`
	ResetButton bool `json:"resetButton"`
	PowerLED    bool `json:"powerLed"`
	// PowerOn is the power LED; meaningful only with PowerLED.
	PowerOn bool `json:"powerOn"`
	HDD     bool `json:"hdd"`
}

// ATXPress is a single button press sent to POST ATXPath.
type ATXPress struct {
	Button     string `json:"button"` // "power" or "reset"
	DurationMs int    `json:"durationMs"`
}

// PressMs is a short press, as the web page's power and reset buttons.
const PressMs = 800

// ATXAction asks the server to execute a complete BMC operation. The daemon
// must not split a cycle into independent pulses: Redfish shares this guard.
type ATXAction struct {
	Action      string `json:"action"`
	HoldSeconds int    `json:"holdSeconds"`
}
