package bmc

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"
)

// fakeATX records presses; a long power press switches a lit host off, a
// short one switches an unlit host on.
type fakeATX struct {
	state   ATXState
	presses []string
}

func (f *fakeATX) State(context.Context) (ATXState, error) { return f.state, nil }

func (f *fakeATX) Press(_ context.Context, button string, ms int) error {
	f.presses = append(f.presses, fmt.Sprintf("%s %d", button, ms))
	if button == "power" && f.state.PowerLED {
		if ms >= 4000 {
			f.state.PowerOn = false
		} else if !f.state.PowerOn {
			f.state.PowerOn = true
		}
	}
	return nil
}

func atxBoard(on bool) *fakeATX {
	return &fakeATX{state: ATXState{PowerButton: true, ResetButton: true, PowerLED: true, PowerOn: on}}
}

func TestPowerActions(t *testing.T) {
	noSleep := func(context.Context, time.Duration) error { return nil }
	cases := []struct {
		name   string
		on     bool
		action func(*Power, context.Context) error
		want   []string
	}{
		{"on when off", false, (*Power).On, []string{"power 800"}},
		{"on when on", true, (*Power).On, nil},
		{"force off when on", true, (*Power).ForceOff, []string{"power 6000"}},
		{"force off when off", false, (*Power).ForceOff, nil},
		{"soft when on", true, (*Power).Soft, []string{"power 800"}},
		{"soft when off", false, (*Power).Soft, nil},
		{"push when off", false, (*Power).Push, []string{"power 800"}},
		{"reset", true, (*Power).Reset, []string{"reset 800"}},
		{"cycle when on", true, (*Power).Cycle, []string{"power 6000", "power 800"}},
		{"cycle when off", false, (*Power).Cycle, []string{"power 800"}},
	}
	for _, c := range cases {
		atx := atxBoard(c.on)
		p := &Power{ATX: atx, Hold: 6 * time.Second, sleep: noSleep}
		if err := c.action(p, context.Background()); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !reflect.DeepEqual(atx.presses, c.want) {
			t.Errorf("%s: presses %v, want %v", c.name, atx.presses, c.want)
		}
	}
}

func TestConfigValidate(t *testing.T) {
	good := Default()
	good.Redfish, good.Password = true, "longenough"
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := []func(*Config){
		func(c *Config) { c.Password = "short" },
		func(c *Config) { c.Password = "a password longer than 20" },
		func(c *Config) { c.Username = "" },
		func(c *Config) { c.Username = "seventeen-chars-x" },
		func(c *Config) { c.Username = "bad name" },
		func(c *Config) { c.IPMIPort = 0 },
		func(c *Config) { c.HoldSeconds = -1 },
		func(c *Config) { c.HoldSeconds = 31 },
		func(c *Config) { c.Password = "with\nnewline" },
	}
	for i, change := range bad {
		c := good
		change(&c)
		if c.Validate() == nil {
			t.Errorf("case %d accepted: %+v", i, c)
		}
	}
	// A disabled add-on needs no password yet.
	if err := Default().Validate(); err != nil {
		t.Fatal(err)
	}
}
