package vm

import (
	"context"
	"errors"
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

// PressATXInternal executes a power action or single press (bmc.ATXPath).
func (s *Service) PressATXInternal(c *gin.Context) {
	pressATXInternal(c, ATX{})
}

func pressATXInternal(c *gin.Context, atx bmc.ATX) {
	var press struct {
		bmc.ATXPress
		bmc.ATXAction
	}
	if err := c.ShouldBindJSON(&press); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	var err error
	if press.Action != "" {
		if press.Button != "" || press.DurationMs != 0 || press.HoldSeconds < 0 || press.HoldSeconds > 30 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid power action"})
			return
		}
		// As with Redfish, finish an accepted action even if the daemon exits.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), time.Minute)
		defer cancel()
		power := bmc.Power{ATX: atx, Hold: time.Duration(press.HoldSeconds) * time.Second}
		err = power.Do(ctx, press.Action)
	} else {
		err = atx.Press(c.Request.Context(), press.Button, press.DurationMs)
	}
	switch {
	case err == nil:
		c.Status(http.StatusNoContent)
	case err == errUnknownButton || err == errPressDuration || errors.Is(err, bmc.ErrUnknownAction):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, bmc.ErrActionBusy):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
	case errors.Is(err, bmc.ErrNoATX), errors.Is(err, bmc.ErrNoReset):
		c.JSON(http.StatusNotImplemented, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "the button press failed"})
	}
}
