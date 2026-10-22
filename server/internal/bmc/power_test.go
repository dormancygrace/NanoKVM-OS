package bmc

import (
	"context"
	"errors"
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

// stuckATX is a host whose power LED goes out only after offAfter state reads
// following the long press (never when offAfter < 0); readErr fails the reads
// after the press.
type stuckATX struct {
	fakeATX
	offAfter int
	readErr  error
	pressed  bool
	reads    int
}

func (s *stuckATX) State(ctx context.Context) (ATXState, error) {
	if !s.pressed {
		return s.state, nil
	}
	if s.readErr != nil {
		return ATXState{}, s.readErr
	}
	s.reads++
	if s.offAfter >= 0 && s.reads > s.offAfter {
		s.state.PowerOn = false
	}
	return s.state, nil
}

func (s *stuckATX) Press(_ context.Context, button string, ms int) error {
	s.pressed = true
	s.presses = append(s.presses, fmt.Sprintf("%s %d", button, ms))
	return nil
}

func TestCycleNeedsTheLEDToGoOut(t *testing.T) {
	noSleep := func(context.Context, time.Duration) error { return nil }
	readErr := errors.New("state read failed")
	cases := []struct {
		name     string
		offAfter int
		readErr  error
		wantErr  error
		want     []string
	}{
		{"stays on", -1, nil, ErrStillOn, []string{"power 5000"}},
		{"goes out late", 10, nil, nil, []string{"power 5000", "power 800"}},
		{"state read fails", 0, readErr, readErr, []string{"power 5000"}},
	}
	for _, c := range cases {
		atx := &stuckATX{fakeATX: *atxBoard(true), offAfter: c.offAfter, readErr: c.readErr}
		p := &Power{ATX: atx, sleep: noSleep}
		if err := p.Cycle(context.Background()); !errors.Is(err, c.wantErr) {
			t.Errorf("%s: error %v, want %v", c.name, err, c.wantErr)
		}
		if !reflect.DeepEqual(atx.presses, c.want) {
			t.Errorf("%s: presses %v, want %v", c.name, atx.presses, c.want)
		}
	}
}

func TestPowerWithoutLEDPressesBlindly(t *testing.T) {
	atx := &fakeATX{state: ATXState{PowerButton: true}}
	p := &Power{ATX: atx, Hold: 5 * time.Second, sleep: func(context.Context, time.Duration) error { return nil }}
	_ = p.On(context.Background())
	_ = p.ForceOff(context.Background())
	if want := []string{"power 800", "power 5000"}; !reflect.DeepEqual(atx.presses, want) {
		t.Fatalf("presses %v, want %v", atx.presses, want)
	}
	if err := p.Reset(context.Background()); !errors.Is(err, ErrNoReset) {
		t.Fatalf("reset without a line: %v", err)
	}
}

func TestHoldDefaultsToTheLongPress(t *testing.T) {
	atx := atxBoard(true)
	p := &Power{ATX: atx}
	if err := p.ForceOff(context.Background()); err != nil || atx.presses[0] != "power 5000" {
		t.Fatalf("presses %v %v", atx.presses, err)
	}
}

func TestPowerWithoutATX(t *testing.T) {
	p := &Power{ATX: &fakeATX{}}
	if err := p.On(context.Background()); !errors.Is(err, ErrNoATX) {
		t.Fatalf("on without ATX: %v", err)
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

func TestConfigSaveLoad(t *testing.T) {
	path := t.TempDir() + "/bmc/config.json"
	c := Default()
	c.IPMI, c.Password = true, "secret-123"
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || got != c {
		t.Fatalf("load: %+v %v", got, err)
	}
}

func TestHTTPPort(t *testing.T) {
	yaml := "proto: https\nport:\n  http: 8080 # plain\n  https: 8443\ncert:\n  http: 1\n"
	if p := httpPort([]byte(yaml)); p != 8080 {
		t.Fatalf("port %d", p)
	}
	if p := httpPort([]byte("proto: http\n")); p != 0 {
		t.Fatalf("port %d without a block", p)
	}
}
