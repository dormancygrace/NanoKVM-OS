package bmc

import (
	"context"
	"errors"
	"time"
)

// ATX is the server's ATX API as the daemon sees it (Client, or a double).
type ATX interface {
	State(ctx context.Context) (ATXState, error)
	Press(ctx context.Context, button string, durationMs int) error
}

var (
	// ErrNoATX: the board has no power button line (NanoKVM Lite).
	ErrNoATX = errors.New("this board has no ATX power control")
	// ErrNoReset: the board has no reset button line.
	ErrNoReset = errors.New("this board has no reset line")
	// ErrStillOn: the power LED stayed lit after the power button was held.
	ErrStillOn = errors.New("the host did not switch off")
)

// Power maps BMC power requests to button presses. Both protocols use it.
// With a power LED, a request that is already met presses nothing; without
// one, every request presses blindly.
type Power struct {
	ATX  ATX
	Hold time.Duration // ForceOff: how long the power button is held

	// For tests.
	sleep func(context.Context, time.Duration) error
}

func (p *Power) wait(ctx context.Context, d time.Duration) error {
	if p.sleep != nil {
		return p.sleep(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (p *Power) holdMs() int {
	hold := p.Hold
	if hold <= 0 {
		hold = DefaultHoldSeconds * time.Second
	}
	return int(hold / time.Millisecond)
}

func (p *Power) state(ctx context.Context) (ATXState, error) {
	state, err := p.ATX.State(ctx)
	if err != nil {
		return state, err
	}
	if !state.PowerButton {
		return state, ErrNoATX
	}
	return state, nil
}

// Status returns whether the host is on, and whether that is known (a board
// with a power LED).
func (p *Power) Status(ctx context.Context) (on, known bool, err error) {
	state, err := p.ATX.State(ctx)
	if err != nil {
		return false, false, err
	}
	return state.PowerOn, state.PowerLED, nil
}

// Available tells whether the board can switch the host at all.
func (p *Power) Available(ctx context.Context) (power, reset bool, err error) {
	state, err := p.ATX.State(ctx)
	return state.PowerButton, state.ResetButton, err
}

// On presses the power button unless the host is known to be on.
func (p *Power) On(ctx context.Context) error {
	state, err := p.state(ctx)
	if err != nil {
		return err
	}
	if state.PowerLED && state.PowerOn {
		return nil
	}
	return p.ATX.Press(ctx, "power", PressMs)
}

// ForceOff holds the power button unless the host is known to be off.
func (p *Power) ForceOff(ctx context.Context) error {
	state, err := p.state(ctx)
	if err != nil {
		return err
	}
	if state.PowerLED && !state.PowerOn {
		return nil
	}
	return p.ATX.Press(ctx, "power", p.holdMs())
}

// Soft presses the power button briefly, so the host's operating system shuts
// down, unless the host is known to be off.
func (p *Power) Soft(ctx context.Context) error {
	state, err := p.state(ctx)
	if err != nil {
		return err
	}
	if state.PowerLED && !state.PowerOn {
		return nil
	}
	return p.ATX.Press(ctx, "power", PressMs)
}

// Push presses the power button briefly, whatever the host's state.
func (p *Power) Push(ctx context.Context) error {
	if _, err := p.state(ctx); err != nil {
		return err
	}
	return p.ATX.Press(ctx, "power", PressMs)
}

// Reset presses the reset button.
func (p *Power) Reset(ctx context.Context) error {
	state, err := p.ATX.State(ctx)
	if err != nil {
		return err
	}
	if !state.ResetButton {
		return ErrNoReset
	}
	return p.ATX.Press(ctx, "reset", PressMs)
}

// Cycle switches the host off and on again; a host known to be off is only
// switched on. With a power LED it waits for the LED to go out (at most
// 10 s) before the second press, then 2 s for the power supply. If the LED
// stays lit, it returns ErrStillOn without the second press.
func (p *Power) Cycle(ctx context.Context) error {
	state, err := p.state(ctx)
	if err != nil {
		return err
	}
	if state.PowerLED && !state.PowerOn {
		return p.ATX.Press(ctx, "power", PressMs)
	}
	if err = p.ATX.Press(ctx, "power", p.holdMs()); err != nil {
		return err
	}
	if state.PowerLED {
		// A host that stays on was not cycled; a short press now would only
		// ask its operating system to shut down.
		off := false
		for i := 0; i < 40 && !off; i++ {
			if i > 0 {
				if err = p.wait(ctx, 250*time.Millisecond); err != nil {
					return err
				}
			}
			if state, err = p.ATX.State(ctx); err != nil {
				return err
			}
			off = !state.PowerOn
		}
		if !off {
			return ErrStillOn
		}
	}
	if err = p.wait(ctx, 2*time.Second); err != nil {
		return err
	}
	return p.ATX.Press(ctx, "power", PressMs)
}
