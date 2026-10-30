package vm

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"NanoKVM-Server/internal/gpioio"
	"github.com/gin-gonic/gin"
)

func TestGPIOSnapshotBroadcastsOnlyChangesWithoutQueues(t *testing.T) {
	monitor := newGPIOInputMonitor(nil, nil, 0, 0, 0)
	_, _, first := monitor.snapshot()
	monitor.publish(gpioInputStatus{}, nil)
	select {
	case <-first:
		t.Fatal("unchanged idle state woke subscribers")
	default:
	}
	monitor.publish(gpioInputStatus{HDD: true}, nil)
	for range 2 { // Every subscriber sees the same notification.
		select {
		case <-first:
		default:
			t.Fatal("subscriber missed activity")
		}
	}
	// No receiver is draining a queue, but publication continues. A slow
	// reader gets today's state rather than replaying an old burst.
	for range 10000 {
		monitor.publish(gpioInputStatus{HDD: true}, nil)
		monitor.publish(gpioInputStatus{Power: true}, nil)
	}
	status, err, next := monitor.snapshot()
	if err != nil || !status.Power || status.HDD {
		t.Fatalf("latest state = %+v, %v", status, err)
	}
	monitor.publish(gpioInputStatus{}, errors.New("read failed"))
	<-next
	_, _, failed := monitor.snapshot()
	monitor.publish(gpioInputStatus{}, errors.New("still failed"))
	select {
	case <-failed:
		t.Fatal("repeated failure woke subscribers")
	default:
	}
	monitor.publish(gpioInputStatus{}, nil)
	<-failed
}

func TestGPIOEventsDeliverShortBlinkAndClearPromptly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	power, hdd := newFakeEdgeInput(true), newFakeEdgeInput(true)
	monitor := newEdgeTestMonitor(t, power, hdd, hddActivityHold, gpioEdgeInterval).withStreamHold(hddStreamHold)
	done := make(chan struct{})
	r := gin.New()
	r.GET("/events", func(c *gin.Context) {
		defer close(done)
		streamGPIOEvents(c, monitor)
	})
	server := httptest.NewServer(r)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/events", nil)
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.Header.Get("Content-Type") != "text/event-stream" || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("stream headers: %v", response.Header)
	}
	scanner := bufio.NewScanner(response.Body)
	read := func() *struct {
		Power bool `json:"pwr"`
		HDD   bool `json:"hdd"`
	} {
		t.Helper()
		for scanner.Scan() {
			if data, ok := strings.CutPrefix(scanner.Text(), "data: "); ok {
				var state *struct {
					Power bool `json:"pwr"`
					HDD   bool `json:"hdd"`
				}
				if err := json.Unmarshal([]byte(data), &state); err != nil {
					t.Fatal(err)
				}
				return state
			}
		}
		t.Fatalf("no snapshot: %v", scanner.Err())
		return nil
	}
	if status := read(); status == nil || status.Power || status.HDD {
		t.Fatalf("initial = %+v", status)
	}
	// A pulse already over before Get() must still be visible, then clear
	// within 200 ms: the old 350 ms hold fails this latency contract.
	start := time.Now()
	hdd.send(t, fakeEdgeEvent{events: gpioio.EdgeEvents{Count: 2, Last: start}})
	if status := read(); status == nil || !status.HDD {
		t.Fatalf("short blink = %+v", status)
	}
	if status := read(); status == nil || status.HDD {
		t.Fatalf("after blink = %+v", status)
	}
	if elapsed := time.Since(start); elapsed >= 200*time.Millisecond {
		t.Fatalf("short blink stretched to %v", elapsed)
	}
	power.set(false)
	power.edge(t)
	if status := read(); status == nil || !status.Power || status.HDD {
		t.Fatalf("power polarity = %+v", status)
	}
	monitor.publish(gpioInputStatus{}, errors.New("GPIO unavailable"))
	if status := read(); status != nil {
		t.Fatalf("read error left stale LEDs: %+v", status)
	}
	monitor.publish(gpioInputStatus{Power: true}, nil)
	if status := read(); status == nil || !status.Power || status.HDD {
		t.Fatalf("stream did not recover: %+v", status)
	}
	monitor.close()
	if status := read(); status != nil {
		t.Fatalf("stopped monitor left stale LEDs: %+v", status)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("stream did not close with monitor")
	}
}

func TestGPIOEventsExitWhenClientLeaves(t *testing.T) {
	power, hdd := newFakeEdgeInput(true), newFakeEdgeInput(true)
	monitor := newEdgeTestMonitor(t, power, hdd, hddActivityHold, gpioEdgeInterval).withStreamHold(hddStreamHold)
	if _, err := monitor.Current(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest("GET", "/events", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() { streamGPIOEvents(c, monitor); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream retained canceled client")
	}
}

func TestGPIOEventsDoNotStartMonitorWithATXOff(t *testing.T) {
	useATX(t, "pcie", "off")
	monitoredGPIOInputs = newGPIOInputMonitor(nil, nil, 0, 0, 0)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/vm/gpio/events", nil)
	(&Service{}).GetGpioEvents(c)
	c.Writer.WriteHeaderNow()
	if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
		t.Fatalf("ATX off: %d %s", w.Code, w.Body.String())
	}
}

func TestGPIOStreamHoldDoesNotShortenSnapshotActivity(t *testing.T) {
	for _, edges := range []bool{true, false} {
		t.Run(map[bool]string{true: "edges", false: "polling"}[edges], func(t *testing.T) {
			power, hdd := newFakeEdgeInput(true), newFakeEdgeInput(true)
			monitor := newLEDMonitor()
			monitor.paths = func() (string, string) { return "power", "hdd" }
			monitor.open = func(path string, output bool) (gpioio.Line, error) {
				if output {
					t.Fatal("requested output")
				}
				if path == "power" {
					return power, nil
				}
				return hdd, nil
			}
			monitor.openEdges = nil
			if edges {
				monitor.openEdges = func(path string) (gpioio.EdgeLine, error) {
					if path == "power" {
						return power, nil
					}
					return hdd, nil
				}
			}
			defer monitor.Close()
			if _, err := monitor.Current(context.Background()); err != nil {
				t.Fatal(err)
			}
			blink := time.Now()
			if edges {
				hdd.send(t, fakeEdgeEvent{events: gpioio.EdgeEvents{Count: 2, Last: blink}})
			} else {
				hdd.set(false)
			}
			waitGPIOStatus(t, monitor, func(s gpioInputStatus) bool { return s.HDD })
			hdd.set(true)
			// The stream clears quickly, but a slower snapshot reader still sees it.
			deadline := blink.Add(200 * time.Millisecond)
			for {
				stream, err, _ := monitor.snapshot()
				if err != nil {
					t.Fatal(err)
				}
				if !stream.HDD {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("stream still active after 200ms")
				}
				time.Sleep(time.Millisecond)
			}
			time.Sleep(time.Until(blink.Add(200 * time.Millisecond)))
			status, err := monitor.Current(context.Background())
			if err != nil || !status.HDD {
				t.Fatalf("snapshot lost its 350ms activity window: %+v, %v", status, err)
			}
			waitGPIOStatus(t, monitor, func(s gpioInputStatus) bool { return !s.HDD })
		})
	}
}
