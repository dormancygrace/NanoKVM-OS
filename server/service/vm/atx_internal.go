package vm

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"NanoKVM-Server/config"
	"NanoKVM-Server/internal/bmc"
)

// ATX is the host's power buttons and LED for the BMC protocols: the
// server's Redfish service directly, the IPMI add-on through bmc.ATXPath.
// It drives the same GPIO lines, pulse lock and LED monitor as the web page.
// Power control from a BMC protocol does not take part in the browser's
// input control: like a hardware BMC, it works whoever holds the keyboard.
type ATX struct{}

func (ATX) State(ctx context.Context) (bmc.ATXState, error) {
	hardware := config.GetInstance().Hardware
	state := bmc.ATXState{
		PowerButton: hardware.GPIOPower != "",
		ResetButton: hardware.GPIOReset != "",
		PowerLED:    hardware.GPIOPowerLED != "",
	}
	if state.PowerLED {
		status, err := monitoredGPIOInputs.Current(ctx)
		if err != nil {
			return state, err
		}
		state.PowerOn, state.HDD = status.Power, status.HDD
	}
	return state, nil
}

// Press holds a button for durationMs (100-60000) and returns once it is
// released.
func (ATX) Press(ctx context.Context, button string, durationMs int) error {
	hardware := config.GetInstance().Hardware
	device := ""
	switch button {
	case "power":
		device = hardware.GPIOPower
		if device == "" {
			return bmc.ErrNoATX
		}
	case "reset":
		device = hardware.GPIOReset
		if device == "" {
			return bmc.ErrNoReset
		}
	default:
		return errUnknownButton
	}
	if durationMs < 100 || durationMs > 60000 {
		return errPressDuration
	}
	return writeGpio(ctx, device, time.Duration(durationMs)*time.Millisecond)
}

type atxError string

func (e atxError) Error() string { return string(e) }

const (
	errUnknownButton atxError = "unknown button"
	errPressDuration atxError = "the press must last 100-60000 ms"
)

// GetATXInternal answers the IPMI add-on's state request (bmc.ATXPath).
func (s *Service) GetATXInternal(c *gin.Context) {
	state, err := ATX{}.State(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "cannot read the ATX LEDs"})
		return
	}
	c.JSON(http.StatusOK, state)
}

// PressATXInternal presses a button for the IPMI add-on (bmc.ATXPath).
func (s *Service) PressATXInternal(c *gin.Context) {
	var press bmc.ATXPress
	if err := c.ShouldBindJSON(&press); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	switch err := (ATX{}).Press(c.Request.Context(), press.Button, press.DurationMs); err {
	case nil:
		c.Status(http.StatusNoContent)
	case errUnknownButton, errPressDuration:
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case bmc.ErrNoATX, bmc.ErrNoReset:
		c.JSON(http.StatusNotImplemented, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "the button press failed"})
	}
}
