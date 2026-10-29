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

// The first start writes the board's default once; a later start, or an
// update's "on" for a Cube, is kept.
func TestEnsureATXSetting(t *testing.T) {
	for board, want := range map[string]string{"pcie": "on\n", "beta": "off\n", "lite": "off\n"} {
		useATX(t, board, "")
		EnsureATXSetting()
		if data, _ := os.ReadFile(atxSettingFile); string(data) != want {
			t.Errorf("%s: first start wrote %q, want %q", board, data, want)
		}
	}
	useATX(t, "beta", "on")
	EnsureATXSetting()
	if data, _ := os.ReadFile(atxSettingFile); string(data) != "on\n" {
		t.Errorf("an existing setting was replaced: %q", data)
	}
	useATX(t, "", "")
	EnsureATXSetting()
	if _, err := os.Stat(atxSettingFile); err == nil {
		t.Error("a board without ATX lines got a setting")
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

// Switching ATX off stops a running LED monitor and leaves a fresh one that
// starts only when asked for after switching on again.
func TestSetATXStopsTheLEDMonitor(t *testing.T) {
	useATX(t, "pcie", "")
	running := newGPIOInputMonitor(nil, nil, 0, 0, 0)
	monitoredGPIOInputs = running

	gin.SetMode(gin.TestMode)
	post := func(body string) string {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest("POST", "/api/vm/atx", bytes.NewBufferString(body))
		c.Request.Header.Set("Content-Type", "application/json")
		(&Service{}).SetATX(c)
		return recorder.Body.String()
	}
	if reply := post(`{"enabled":false}`); !strings.Contains(reply, `"enabled":false`) || ATXEnabled() {
		t.Fatalf("switch off: %s", reply)
	}
	select {
	case <-running.stop:
	default:
		t.Fatal("the running monitor was not stopped")
	}
	if gpioMonitor() == running {
		t.Fatal("the stopped monitor is still the current one")
	}
	if reply := post(`{"enabled":true}`); !strings.Contains(reply, `"enabled":true`) || !ATXEnabled() {
		t.Fatalf("switch on: %s", reply)
	}
	if reply := post(`{}`); !strings.Contains(reply, "enabled is required") {
		t.Fatalf("no value: %s", reply)
	}
	useATX(t, "", "")
	if reply := post(`{"enabled":true}`); !strings.Contains(reply, "no known ATX lines") {
		t.Fatalf("switch on without lines: %s", reply)
	}
}
