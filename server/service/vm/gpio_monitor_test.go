package vm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
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

type fakeEdgeEvent struct {
	events gpioio.EdgeEvents
	err    error
}

// fakeEdgeInput is an edge line whose events the test injects. With storm
// set, every wait returns at once, as for a line that never stops toggling.
type fakeEdgeInput struct {
	fakeGPIOInput
	events    chan fakeEdgeEvent
	closed    chan struct{}
	closeOnce sync.Once
	storm     bool
	gets      atomic.Int32
	waits     atomic.Int32
}

func newFakeEdgeInput(high bool) *fakeEdgeInput {
	return &fakeEdgeInput{
		fakeGPIOInput: fakeGPIOInput{high: high},
		events:        make(chan fakeEdgeEvent),
		closed:        make(chan struct{}),
	}
}

func (f *fakeEdgeInput) Get() (bool, error) {
	f.gets.Add(1)
	return f.fakeGPIOInput.Get()
}

func (f *fakeEdgeInput) Close() error {
	f.closeOnce.Do(func() { close(f.closed) })
	return nil
}

func (f *fakeEdgeInput) WaitEdges() (gpioio.EdgeEvents, error) {
	f.waits.Add(1)
	if f.storm {
		select {
		case <-f.closed:
			return gpioio.EdgeEvents{}, os.ErrClosed
		default:
			return gpioio.EdgeEvents{Count: 64, Last: time.Now()}, nil
		}
	}
	select {
	case event := <-f.events:
		return event.events, event.err
	case <-f.closed:
		return gpioio.EdgeEvents{}, os.ErrClosed
	}
}

func (f *fakeEdgeInput) edge(t *testing.T) {
	t.Helper()
	f.send(t, fakeEdgeEvent{events: gpioio.EdgeEvents{Count: 1, Last: time.Now()}})
}

func (f *fakeEdgeInput) send(t *testing.T, event fakeEdgeEvent) {
	t.Helper()
	select {
	case f.events <- event:
	case <-time.After(time.Second):
		t.Fatal("monitor is not waiting for edge events")
	}
}

func newEdgeTestMonitor(t *testing.T, power, hdd *fakeEdgeInput, hold, interval time.Duration) *gpioInputMonitor {
	t.Helper()
	monitor := newGPIOInputMonitor(
		func() (string, string) { return "power", "hdd" },
		func(string, bool) (gpioio.Line, error) {
			t.Error("edge-capable line was polled")
			return nil, errors.New("unexpected polling")
		},
		time.Millisecond,
		hold,
		time.Millisecond,
	).withEdges(func(path string) (gpioio.EdgeLine, error) {
		if path == "power" {
			return power, nil
		}
		return hdd, nil
	}, interval)
	t.Cleanup(func() {
		monitor.Close()
		select {
		case <-power.closed:
		default:
			t.Error("power line left open")
		}
		select {
		case <-hdd.closed:
		default:
			t.Error("HDD line left open")
		}
	})
	return monitor
}

func TestGPIOEdgeMonitorPublishesOnEventsOnly(t *testing.T) {
	power, hdd := newFakeEdgeInput(true), newFakeEdgeInput(true)
	monitor := newEdgeTestMonitor(t, power, hdd, time.Hour, time.Millisecond)

	status, err := monitor.Current(context.Background())
	if err != nil || status.Power || status.HDD {
		t.Fatalf("initial status = %+v, err = %v", status, err)
	}
	time.Sleep(30 * time.Millisecond)
	if gets := power.gets.Load() + hdd.gets.Load(); gets != 2 {
		t.Fatalf("idle monitor read the lines %d times, want 2", gets)
	}

	power.set(false)
	power.edge(t)
	waitGPIOStatus(t, monitor, func(status gpioInputStatus) bool { return status.Power && !status.HDD })

	power.set(true)
	power.edge(t)
	waitGPIOStatus(t, monitor, func(status gpioInputStatus) bool { return !status.Power })
	time.Sleep(30 * time.Millisecond)
	if gets := power.gets.Load(); gets != 3 {
		t.Fatalf("power read %d times for two events, want 3", gets)
	}
}

func TestGPIOEdgeMonitorHoldsHDDActivityUntilHoldExpires(t *testing.T) {
	const hold = 60 * time.Millisecond
	power, hdd := newFakeEdgeInput(true), newFakeEdgeInput(true)
	monitor := newEdgeTestMonitor(t, power, hdd, hold, time.Millisecond)
	if _, err := monitor.Current(context.Background()); err != nil {
		t.Fatal(err)
	}

	// A blink that ended before the levels were read still counts.
	blink := time.Now()
	hdd.send(t, fakeEdgeEvent{events: gpioio.EdgeEvents{Count: 2, Last: blink}})
	waitGPIOStatus(t, monitor, func(status gpioInputStatus) bool { return status.HDD })
	waitGPIOStatus(t, monitor, func(status gpioInputStatus) bool { return !status.HDD })
	if held := time.Since(blink); held < hold {
		t.Fatalf("HDD activity cleared after %v, hold is %v", held, hold)
	}

	// A lit LED stays active, and the hold runs from its rising edge.
	hdd.set(false)
	hdd.edge(t)
	waitGPIOStatus(t, monitor, func(status gpioInputStatus) bool { return status.HDD })
	time.Sleep(2 * hold)
	if status, _ := monitor.Current(context.Background()); !status.HDD {
		t.Fatal("lit HDD LED reported inactive")
	}
	hdd.set(true)
	off := time.Now()
	hdd.send(t, fakeEdgeEvent{events: gpioio.EdgeEvents{Count: 1, Last: off}})
	waitGPIOStatus(t, monitor, func(status gpioInputStatus) bool { return !status.HDD })
	if held := time.Since(off); held < hold {
		t.Fatalf("HDD activity cleared %v after the LED went out, hold is %v", held, hold)
	}
	// Initial read, then one per event and one per expired hold.
	if gets := hdd.gets.Load(); gets != 6 {
		t.Fatalf("HDD read %d times, want 6", gets)
	}
}

func TestGPIOEdgeMonitorRateLimitsEventReads(t *testing.T) {
	const interval = 25 * time.Millisecond
	power, hdd := newFakeEdgeInput(true), newFakeEdgeInput(true)
	hdd.storm = true
	monitor := newEdgeTestMonitor(t, power, hdd, time.Hour, interval)

	waitGPIOStatus(t, monitor, func(status gpioInputStatus) bool { return status.HDD })
	start, startWaits := time.Now(), hdd.waits.Load()
	time.Sleep(10 * interval)
	waits := hdd.waits.Load() - startWaits
	if limit := int32(time.Since(start)/interval) + 1; waits < 2 || waits > limit {
		t.Fatalf("storming line serviced %d times in %v, want 2..%d", waits, time.Since(start), limit)
	}
	if gets := hdd.gets.Load(); gets > 2*waits+2 {
		t.Fatalf("HDD read %d times for %d services", gets, waits)
	}
}

func TestGPIOInputMonitorPollsWhenEdgesAreUnsupported(t *testing.T) {
	unsupported := fmt.Errorf("%w: test chip has no interrupts", gpioio.ErrEdgesUnsupported)
	for _, tc := range []struct {
		name string
		open func(*fakeEdgeInput) gpioEdgeOpen
	}{
		{"request", func(*fakeEdgeInput) gpioEdgeOpen {
			return func(string) (gpioio.EdgeLine, error) { return nil, unsupported }
		}},
		{"read", func(line *fakeEdgeInput) gpioEdgeOpen {
			line.events = make(chan fakeEdgeEvent, 1)
			line.events <- fakeEdgeEvent{err: unsupported}
			return func(string) (gpioio.EdgeLine, error) { return line, nil }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			edgeLine := newFakeEdgeInput(true)
			var edgeOpens atomic.Int32
			openEdges := tc.open(edgeLine)
			power := &fakeGPIOInput{high: true}
			monitor := newGPIOInputMonitor(
				func() (string, string) { return "power", "" },
				func(string, bool) (gpioio.Line, error) { return power, nil },
				time.Millisecond,
				time.Millisecond,
				time.Hour, // Falling back must not wait for a retry.
			).withEdges(func(path string) (gpioio.EdgeLine, error) {
				edgeOpens.Add(1)
				return openEdges(path)
			}, time.Millisecond)
			defer monitor.Close()

			power.set(false)
			waitGPIOStatus(t, monitor, func(status gpioInputStatus) bool { return status.Power })
			power.set(true)
			waitGPIOStatus(t, monitor, func(status gpioInputStatus) bool { return !status.Power })
			if n := edgeOpens.Load(); n != 1 {
				t.Fatalf("edge requests = %d, want 1", n)
			}
			if tc.name == "read" {
				select {
				case <-edgeLine.closed:
				default:
					t.Fatal("edge line left open")
				}
			}
		})
	}
}

func TestGPIOEdgeMonitorRetriesOtherOpenErrors(t *testing.T) {
	power, hdd := newFakeEdgeInput(false), newFakeEdgeInput(true)
	var opens atomic.Int32
	monitor := newGPIOInputMonitor(
		func() (string, string) { return "power", "hdd" },
		func(string, bool) (gpioio.Line, error) { return nil, errors.New("unexpected polling") },
		time.Millisecond,
		time.Millisecond,
		time.Millisecond,
	).withEdges(func(path string) (gpioio.EdgeLine, error) {
		if opens.Add(1) == 1 {
			return nil, errors.New("device or resource busy")
		}
		if path == "power" {
			return power, nil
		}
		return hdd, nil
	}, time.Millisecond)
	defer monitor.Close()

	deadline := time.Now().Add(time.Second)
	for {
		status, err := monitor.Current(context.Background())
		if err == nil && status.Power {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("status = %+v, err = %v", status, err)
		}
		time.Sleep(time.Millisecond)
	}
	if n := opens.Load(); n != 3 {
		t.Fatalf("edge requests = %d, want 3", n)
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
