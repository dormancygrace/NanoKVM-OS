package vm

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"NanoKVM-Server/internal/gpioio"
)

type fakeGPIOInput struct {
	mu   sync.RWMutex
	high bool
}

func (f *fakeGPIOInput) Set(bool) error { return errors.New("input cannot be set") }
func (f *fakeGPIOInput) Close() error   { return nil }
func (f *fakeGPIOInput) Get() (bool, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.high, nil
}
func (f *fakeGPIOInput) set(high bool) {
	f.mu.Lock()
	f.high = high
	f.mu.Unlock()
}

func TestGPIOInputMonitorTracksPowerAndHoldsHDDActivity(t *testing.T) {
	power := &fakeGPIOInput{high: true}
	hdd := &fakeGPIOInput{high: true}
	open := func(path string, output bool) (gpioio.Line, error) {
		if output {
			t.Fatal("input requested as output")
		}
		switch path {
		case "power":
			return power, nil
		case "hdd":
			return hdd, nil
		default:
			return nil, errors.New("unexpected path")
		}
	}
	monitor := newGPIOInputMonitor(
		func() (string, string) { return "power", "hdd" },
		open,
		time.Millisecond,
		20*time.Millisecond,
		time.Millisecond,
	)
	defer monitor.Close()

	status, err := monitor.Current(context.Background())
	if err != nil || status.Power || status.HDD {
		t.Fatalf("initial status = %+v, err = %v", status, err)
	}

	power.set(false)
	hdd.set(false)
	waitGPIOStatus(t, monitor, func(status gpioInputStatus) bool {
		return status.Power && status.HDD
	})

	hdd.set(true)
	status, err = monitor.Current(context.Background())
	if err != nil || !status.HDD {
		t.Fatalf("HDD activity was not held: %+v, err = %v", status, err)
	}
	waitGPIOStatus(t, monitor, func(status gpioInputStatus) bool {
		return status.Power && !status.HDD
	})
}

func TestGPIOInputMonitorAllowsMissingHDDInput(t *testing.T) {
	power := &fakeGPIOInput{high: false}
	monitor := newGPIOInputMonitor(
		func() (string, string) { return "power", "" },
		func(path string, output bool) (gpioio.Line, error) { return power, nil },
		time.Millisecond,
		time.Millisecond,
		time.Millisecond,
	)
	defer monitor.Close()

	status, err := monitor.Current(context.Background())
	if err != nil || !status.Power || status.HDD {
		t.Fatalf("status = %+v, err = %v", status, err)
	}
}

func waitGPIOStatus(t *testing.T, monitor *gpioInputMonitor, accept func(gpioInputStatus) bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status, err := monitor.Current(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if accept(status) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for GPIO status")
}
