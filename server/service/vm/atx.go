package vm

import (
	"errors"
	"os"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/config"
	"NanoKVM-Server/internal/atomicfile"
	"NanoKVM-Server/proto"
)

// atxSettingFile is the ATX switch (System > Device): "on" or "off" for the
// host's power and reset buttons and its power and HDD LEDs. Absent: the
// board's default, written at the first start (EnsureATXSetting). Cube and
// Lite need the KVM-B board for ATX, so they start off; PCIe has it on the
// card. nanokvm-base's post-upgrade writes "on" for Cubes updated from a
// release without the switch, which had ATX on.
var atxSettingFile = "/etc/kvm/atx"

func atxDefault(board string) bool { return board == "pcie" }

// atxAvailable: the board has known ATX lines.
func atxAvailable() bool { return config.GetInstance().Hardware.GPIOPower != "" }

// ATXEnabled reports the switch: the board's lines are used.
func ATXEnabled() bool {
	if !atxAvailable() {
		return false
	}
	data, err := os.ReadFile(atxSettingFile)
	switch strings.TrimSpace(string(data)) {
	case "on":
		return true
	case "off":
		return false
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Errorf("ATX setting: %v", err)
	}
	return atxDefault(config.GetInstance().Hardware.Board)
}

// atxHardware is the board's hardware with its ATX lines only while the
// switch is on: the buttons, the LED monitor, Redfish, IPMI and MCP all go
// through it.
func atxHardware() config.Hardware {
	hardware := config.GetInstance().Hardware
	if !ATXEnabled() {
		hardware.GPIOPower, hardware.GPIOReset, hardware.GPIOPowerLED, hardware.GPIOHDDLed = "", "", "", ""
	}
	return hardware
}

// EnsureATXSetting writes the board's default at the first start, so a later
// update cannot mistake a new installation for one that had ATX before.
func EnsureATXSetting() {
	if _, err := os.Stat(atxSettingFile); !errors.Is(err, os.ErrNotExist) || !atxAvailable() {
		return
	}
	if err := writeATXSetting(atxDefault(config.GetInstance().Hardware.Board)); err != nil {
		log.Errorf("ATX setting: %v", err)
	}
}

func writeATXSetting(on bool) error {
	value := "off\n"
	if on {
		value = "on\n"
	}
	return atomicfile.Write(atxSettingFile, []byte(value), 0o644)
}

// The LED monitor runs while ATX is on and someone asks for the LEDs;
// switching ATX off stops it, and the next request after switching on starts
// a fresh one.
var gpioMonitorMu sync.Mutex

func gpioMonitor() *gpioInputMonitor {
	gpioMonitorMu.Lock()
	defer gpioMonitorMu.Unlock()
	return monitoredGPIOInputs
}

func stopGPIOMonitor() {
	gpioMonitorMu.Lock()
	old := monitoredGPIOInputs
	monitoredGPIOInputs = newLEDMonitor()
	gpioMonitorMu.Unlock()
	old.close()
}

// GetATX answers GET /api/vm/atx: the switch, whether the board has ATX
// lines, and its default.
func (s *Service) GetATX(c *gin.Context) {
	var rsp proto.Response
	hardware := config.GetInstance().Hardware
	rsp.OkRspWithData(c, gin.H{
		"enabled":   ATXEnabled(),
		"available": atxAvailable(),
		"default":   atxDefault(hardware.Board),
		"board":     hardware.Board,
	})
}

// SetATX answers POST /api/vm/atx {"enabled":bool}.
func (s *Service) SetATX(c *gin.Context) {
	var rsp proto.Response
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		rsp.ErrRsp(c, -1, "enabled is required")
		return
	}
	if *req.Enabled && !atxAvailable() {
		rsp.ErrRsp(c, -2, "this board has no known ATX lines")
		return
	}
	if err := writeATXSetting(*req.Enabled); err != nil {
		log.Errorf("ATX setting: %v", err)
		rsp.ErrRsp(c, -3, "the setting could not be saved")
		return
	}
	if !*req.Enabled {
		stopGPIOMonitor()
	}
	log.Infof("ATX power control %s", map[bool]string{true: "on", false: "off"}[*req.Enabled])
	rsp.OkRspWithData(c, gin.H{"enabled": ATXEnabled()})
}
