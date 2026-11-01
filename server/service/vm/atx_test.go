package vm

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"NanoKVM-Server/config"
)

// useATX gives the test a board and an ATX setting file of its own;
// setting "" leaves the file absent.
func useATX(t *testing.T, board, setting string) {
	t.Helper()
	hardware := &config.GetInstance().Hardware
	previousHardware, previousFile, previousMonitor := *hardware, atxSettingFile, monitoredGPIOInputs
	t.Cleanup(func() {
		*hardware, atxSettingFile, monitoredGPIOInputs = previousHardware, previousFile, previousMonitor
	})
	hardware.Board = board
	if board == "" {
		hardware.GPIOPower, hardware.GPIOReset, hardware.GPIOPowerLED, hardware.GPIOHDDLed = "", "", "", ""
	} else {
		hardware.GPIOPower, hardware.GPIOReset, hardware.GPIOPowerLED = "power", "reset", "led"
	}
	atxSettingFile = filepath.Join(t.TempDir(), "atx")
	if setting != "" {
		if err := os.WriteFile(atxSettingFile, []byte(setting+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestATXDefaultsPerBoard(t *testing.T) {
	for _, c := range []struct {
		board, setting string
		want           bool
	}{
		{"pcie", "", true}, {"beta", "", false}, {"alpha", "", false}, {"lite", "", false},
		{"beta", "on", true}, {"lite", "on", true}, {"pcie", "off", false}, {"beta", "garbage", false},
		{"", "on", false}, // no known lines: never on
	} {
		useATX(t, c.board, c.setting)
		if got := ATXEnabled(); got != c.want {
			t.Errorf("board %q setting %q: enabled %v, want %v", c.board, c.setting, got, c.want)
		}
		hardware := atxHardware()
		if (hardware.GPIOPower != "") != c.want || (hardware.GPIOPowerLED != "") != c.want {
			t.Errorf("board %q setting %q: lines %+v", c.board, c.setting, hardware)
		}
	}
}

func TestATXOffPressesNothing(t *testing.T) {
	useATX(t, "beta", "off")
	state, err := ATX{}.State(context.Background())
	if err != nil || state.PowerButton || state.ResetButton || state.PowerLED || state.PowerOn {
		t.Fatalf("state with ATX off: %+v %v", state, err)
	}
	if err := (ATX{}).Press(context.Background(), "power", 200); err == nil {
		t.Fatal("power pressed with ATX off")
	}

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/api/vm/gpio", bytes.NewBufferString(`{"type":"power"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	(&Service{}).SetGpio(c)
	if !strings.Contains(recorder.Body.String(), "ATX power control is off") {
		t.Fatalf("SetGpio with ATX off: %s", recorder.Body.String())
	}
}
