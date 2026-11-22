package bmc

// ATXState is the host's power lines as the BMC protocols see them.
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

// PressMs is a short press, as the web page's power and reset buttons.
const PressMs = 800
