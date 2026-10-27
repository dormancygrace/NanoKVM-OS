package vm

import (
	"context"
	"path/filepath"
	"testing"

	"NanoKVM-Server/config"
	"NanoKVM-Server/pkg/bmc"
)

// With "power LED connected" off, the BMC side sees no power LED: the state
// is unknown and requests press, whatever the LED line reads.
func TestATXStateFollowsThePowerLEDSetting(t *testing.T) {
	hardware := &config.GetInstance().Hardware
	previousHardware, previousMonitor, previousPath := *hardware, monitoredGPIOInputs, bmcConfigPath
	t.Cleanup(func() {
		*hardware, monitoredGPIOInputs, bmcConfigPath = previousHardware, previousMonitor, previousPath
	})
	hardware.GPIOPower, hardware.GPIOReset, hardware.GPIOPowerLED = "power", "reset", "led"

	lit := newGPIOInputMonitor(nil, nil, 0, 0, 0)
	lit.startOnce.Do(func() {})
	lit.status = gpioInputStatus{Power: true, HDD: true}
	close(lit.ready)
	monitoredGPIOInputs = lit
	bmcConfigPath = filepath.Join(t.TempDir(), "config.json")

	state, err := ATX{}.State(context.Background())
	if err != nil || !state.PowerLED || !state.PowerOn || !state.HDD {
		t.Fatalf("connected LED: %+v %v", state, err)
	}

	settings := bmc.Default()
	settings.PowerLEDDisconnected = true
	if err := bmc.Save(bmcConfigPath, settings); err != nil {
		t.Fatal(err)
	}
	state, err = ATX{}.State(context.Background())
	if err != nil || state.PowerLED || state.PowerOn || !state.HDD || !state.PowerButton {
		t.Fatalf("disconnected LED: %+v %v", state, err)
	}
}
