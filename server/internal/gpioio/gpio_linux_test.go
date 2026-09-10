package gpioio

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type fakeLine struct {
	mu                              sync.Mutex
	values                          []bool
	closed                          bool
	activated                       chan struct{}
	activeErr, releaseErr, closeErr error
}

func (f *fakeLine) Set(value bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values = append(f.values, value)
	if value {
		if f.activated != nil {
			close(f.activated)
		}
		return f.activeErr
	}
	return f.releaseErr
}
func (f *fakeLine) Get() (bool, error) { return false, nil }
func (f *fakeLine) Close() error       { f.closed = true; return f.closeErr }

func TestPulseCancelReleasesAndRejectsOverlap(t *testing.T) {
	var c Controller
	f := &fakeLine{activated: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Pulse(ctx, time.Minute, func() (Line, error) { return f, nil }) }()
	select {
	case <-f.activated:
	case <-time.After(time.Second):
		t.Fatal("pulse not activated")
	}
	if err := c.Pulse(context.Background(), time.Millisecond, func() (Line, error) { t.Fatal("overlap requested line"); return nil, nil }); !errors.Is(err, ErrBusy) {
		t.Fatalf("overlap: %v", err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(f.values) != 2 || !f.values[0] || f.values[1] || !f.closed {
		t.Fatalf("not released: %+v", f)
	}
}

func TestPulseFailureStillReleasesAndReportsCleanupFailure(t *testing.T) {
	a, r, z := errors.New("activate"), errors.New("release"), errors.New("close")
	f := &fakeLine{activeErr: a, releaseErr: r, closeErr: z}
	err := new(Controller).Pulse(context.Background(), time.Millisecond, func() (Line, error) { return f, nil })
	for _, want := range []error{a, r, z} {
		if !errors.Is(err, want) {
			t.Fatalf("missing %v from %v", want, err)
		}
	}
	if len(f.values) != 2 || f.values[1] || !f.closed {
		t.Fatal("failed activation left line unreleased")
	}
}

func TestPulseInvalidAndCancelledDoNotRequestLines(t *testing.T) {
	c := new(Controller)
	request := func() (Line, error) { t.Fatal("unexpected hardware request"); return nil, nil }
	for _, d := range []time.Duration{0, -1, time.Minute + 1} {
		if c.Pulse(context.Background(), d, request) == nil {
			t.Fatal("invalid duration accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Pulse(ctx, time.Second, request); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestOutputRequestStartsReleased(t *testing.T) {
	r := request(23, true)
	if r.Num_lines != 1 || r.Offsets[0] != 23 || r.Config.Flags != flagOutput || r.Config.Num_attrs != 1 {
		t.Fatal(r)
	}
	a := r.Config.Attrs[0]
	if a.Mask != 1 || a.Attr.Id != unix.GPIO_V2_LINE_ATTR_ID_OUTPUT_VALUES || a.Attr.Flags != 0 {
		t.Fatal("initial output is not explicitly inactive")
	}
	r = request(24, false)
	if r.Config.Flags != flagInput || r.Config.Num_attrs != 0 {
		t.Fatal("LED request changes output state")
	}
}

func TestPulseTimerReleasesLine(t *testing.T) {
	f := &fakeLine{}
	err := new(Controller).Pulse(context.Background(), time.Millisecond, func() (Line, error) { return f, nil })
	if err != nil || len(f.values) != 2 || !f.values[0] || f.values[1] || !f.closed {
		t.Fatalf("timer release failed: %v %+v", err, f)
	}
}
