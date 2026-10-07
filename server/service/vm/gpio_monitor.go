package vm

import (
	"context"
	"errors"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/config"
	"NanoKVM-Server/internal/gpioio"
)

const (
	gpioInputSampleInterval = 20 * time.Millisecond
	hddActivityHold         = 350 * time.Millisecond
	gpioInputRetryInterval  = time.Second
	// gpioEdgeInterval limits how often a line's event queue is serviced, so
	// a fast-blinking HDD LED costs at most one read per line per interval.
	gpioEdgeInterval = 50 * time.Millisecond
)

type gpioInputStatus struct {
	Power bool
	HDD   bool
}

type gpioInputPaths func() (power, hdd string)
type gpioLineOpen func(device string, output bool) (gpioio.Line, error)
type gpioEdgeOpen func(device string) (gpioio.EdgeLine, error)

type gpioMonitorMode int

const (
	gpioModeUnknown gpioMonitorMode = iota
	gpioModeEdges
	gpioModePolling
)

type gpioInputMonitor struct {
	paths     gpioInputPaths
	open      gpioLineOpen
	openEdges gpioEdgeOpen // nil: always poll

	sampleInterval time.Duration
	hddHold        time.Duration
	retryInterval  time.Duration
	edgeInterval   time.Duration

	// Owned by the run goroutine.
	edgesUnsupported bool
	loggedMode       gpioMonitorMode

	startOnce sync.Once
	readyOnce sync.Once
	stopOnce  sync.Once
	ready     chan struct{}
	stop      chan struct{}
	stopped   chan struct{}

	mu     sync.RWMutex
	status gpioInputStatus
	err    error
}

var monitoredGPIOInputs = newGPIOInputMonitor(
	func() (string, string) {
		hardware := config.GetInstance().Hardware
		return hardware.GPIOPowerLED, hardware.GPIOHDDLed
	},
	gpioio.Open,
	gpioInputSampleInterval,
	hddActivityHold,
	gpioInputRetryInterval,
).withEdges(gpioio.OpenEdges, gpioEdgeInterval)

func newGPIOInputMonitor(paths gpioInputPaths, open gpioLineOpen, sampleInterval, hddHold, retryInterval time.Duration) *gpioInputMonitor {
	return &gpioInputMonitor{
		paths:          paths,
		open:           open,
		sampleInterval: sampleInterval,
		hddHold:        hddHold,
		retryInterval:  retryInterval,
		ready:          make(chan struct{}),
		stop:           make(chan struct{}),
		stopped:        make(chan struct{}),
	}
}

// withEdges makes the monitor wait for edge events instead of polling, when
// the lines support them, servicing each line at most once per interval.
func (m *gpioInputMonitor) withEdges(open gpioEdgeOpen, interval time.Duration) *gpioInputMonitor {
	m.openEdges = open
	m.edgeInterval = interval
	return m
}

func (m *gpioInputMonitor) Current(ctx context.Context) (gpioInputStatus, error) {
	m.startOnce.Do(func() { go m.run() })

	select {
	case <-ctx.Done():
		return gpioInputStatus{}, ctx.Err()
	case <-m.ready:
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status, m.err
}

func (m *gpioInputMonitor) Close() {
	m.stopOnce.Do(func() { close(m.stop) })
	<-m.stopped
}

func (m *gpioInputMonitor) run() {
	defer close(m.stopped)
	for {
		power, hdd, edges, err := m.openInputs()
		if err != nil {
			m.publish(gpioInputStatus{}, err)
			if !m.waitRetry() {
				return
			}
			continue
		}

		var retry bool
		if edges {
			var hddEdges gpioio.EdgeLine
			if hdd != nil {
				hddEdges = hdd.(gpioio.EdgeLine)
			}
			retry, err = m.watchEdges(power.(gpioio.EdgeLine), hddEdges)
		} else {
			retry = m.sampleInputs(power, hdd)
			err = errors.Join(power.Close(), closeLine(hdd))
		}
		if err != nil {
			m.publish(gpioInputStatus{}, err)
		}
		if edges && m.edgesUnsupported && retry {
			continue // Reopen at once, for polling.
		}
		if !retry || !m.waitRetry() {
			return
		}
	}
}

// openInputs requests edge-reporting lines when possible and plain input
// lines otherwise; edges reports which kind it returned.
func (m *gpioInputMonitor) openInputs() (power, hdd gpioio.Line, edges bool, err error) {
	powerPath, hddPath := m.paths()
	if m.openEdges != nil && !m.edgesUnsupported {
		power, hdd, err := m.openEdgeInputs(powerPath, hddPath)
		if err == nil {
			m.logMode(gpioModeEdges, nil)
			return power, hdd, true, nil
		}
		if !errors.Is(err, gpioio.ErrEdgesUnsupported) {
			return nil, nil, false, err
		}
		m.edgesUnsupported = true
		m.logMode(gpioModePolling, err)
	}

	power, err = m.open(powerPath, false)
	if err != nil {
		return nil, nil, false, err
	}
	m.logMode(gpioModePolling, nil)
	if hddPath == "" {
		return power, nil, false, nil
	}
	hdd, err = m.open(hddPath, false)
	if err != nil {
		return nil, nil, false, errors.Join(err, power.Close())
	}
	return power, hdd, false, nil
}

func (m *gpioInputMonitor) openEdgeInputs(powerPath, hddPath string) (gpioio.Line, gpioio.Line, error) {
	power, err := m.openEdges(powerPath)
	if err != nil {
		return nil, nil, err
	}
	if hddPath == "" {
		return power, nil, nil
	}
	hdd, err := m.openEdges(hddPath)
	if err != nil {
		return nil, nil, errors.Join(err, power.Close())
	}
	return power, hdd, nil
}

func (m *gpioInputMonitor) logMode(mode gpioMonitorMode, reason error) {
	if mode == m.loggedMode {
		return
	}
	m.loggedMode = mode
	switch {
	case mode == gpioModeEdges:
		log.Infof("ATX LED monitor: waiting for GPIO edge events, serviced at most every %v", m.edgeInterval)
	case reason != nil:
		log.Infof("ATX LED monitor: polling every %v: %v", m.sampleInterval, reason)
	default:
		log.Infof("ATX LED monitor: polling every %v", m.sampleInterval)
	}
}

func (m *gpioInputMonitor) sampleInputs(power, hdd gpioio.Line) bool {
	ticker := time.NewTicker(m.sampleInterval)
	defer ticker.Stop()
	var hddUntil time.Time

	for {
		powerHigh, err := power.Get()
		if err != nil {
			m.publish(gpioInputStatus{}, err)
			return true
		}

		now := time.Now()
		hddActive := false
		if hdd != nil {
			hddHigh, err := hdd.Get()
			if err != nil {
				m.publish(gpioInputStatus{}, err)
				return true
			}
			if !hddHigh {
				hddUntil = now.Add(m.hddHold)
			}
			hddActive = !hddHigh || now.Before(hddUntil)
		}
		m.publish(gpioInputStatus{Power: !powerHigh, HDD: hddActive}, nil)

		select {
		case <-m.stop:
			return false
		case <-ticker.C:
		}
	}
}

type gpioEdgeUpdate struct {
	hdd    bool
	events gpioio.EdgeEvents
	err    error
}

// watchEdges publishes the levels whenever a line reports edges, and once
// more when the HDD hold expires. It closes both lines before returning.
func (m *gpioInputMonitor) watchEdges(power, hdd gpioio.EdgeLine) (retry bool, err error) {
	updates := make(chan gpioEdgeUpdate)
	done := make(chan struct{})
	var readers sync.WaitGroup
	readers.Go(func() { m.readEdges(power, false, updates, done) })
	if hdd != nil {
		readers.Go(func() { m.readEdges(hdd, true, updates, done) })
	}
	defer func() {
		close(done)
		// Closing the lines wakes readers parked in the netpoller.
		var hddErr error
		if hdd != nil {
			hddErr = hdd.Close()
		}
		err = errors.Join(err, power.Close(), hddErr)
		readers.Wait()
	}()

	hold := time.NewTimer(time.Hour)
	hold.Stop()
	defer hold.Stop()
	var hddUntil time.Time

	publishLevels := func() bool {
		powerHigh, err := power.Get()
		if err != nil {
			m.publish(gpioInputStatus{}, err)
			return false
		}
		now := time.Now()
		hddActive := false
		if hdd != nil {
			hddHigh, err := hdd.Get()
			if err != nil {
				m.publish(gpioInputStatus{}, err)
				return false
			}
			if !hddHigh {
				hddUntil = now.Add(m.hddHold)
			}
			hddActive = !hddHigh || now.Before(hddUntil)
			// While the LED is lit its rising edge will report; after that
			// only the end of the hold needs a wakeup.
			hold.Stop()
			if hddHigh && hddActive {
				hold.Reset(hddUntil.Sub(now))
			}
		}
		m.publish(gpioInputStatus{Power: !powerHigh, HDD: hddActive}, nil)
		return true
	}

	if !publishLevels() {
		return true, nil
	}
	for {
		select {
		case <-m.stop:
			return false, nil
		case update := <-updates:
			if update.err != nil {
				if errors.Is(update.err, gpioio.ErrEdgesUnsupported) {
					m.edgesUnsupported = true
					m.logMode(gpioModePolling, update.err)
					return true, nil
				}
				m.publish(gpioInputStatus{}, update.err)
				return true, nil
			}
			// Any HDD edge means the LED was lit at that moment, even when
			// the blink ended before the levels are read.
			if update.hdd && update.events.Count > 0 {
				if until := update.events.Last.Add(m.hddHold); until.After(hddUntil) {
					hddUntil = until
				}
			}
		case <-hold.C:
		}
		if !publishLevels() {
			return true, nil
		}
	}
}

// readEdges forwards the edge events of one line, draining its queue at most
// once per edgeInterval however fast the line toggles. Events the kernel
// drops meanwhile do not matter: every update re-reads the levels.
func (m *gpioInputMonitor) readEdges(line gpioio.EdgeLine, hdd bool, updates chan<- gpioEdgeUpdate, done <-chan struct{}) {
	pause := time.NewTimer(time.Hour)
	pause.Stop()
	defer pause.Stop()
	for {
		events, err := line.WaitEdges()
		select {
		case updates <- gpioEdgeUpdate{hdd: hdd, events: events, err: err}:
		case <-done:
			return
		}
		if err != nil {
			return
		}
		pause.Reset(m.edgeInterval)
		select {
		case <-pause.C:
		case <-done:
			return
		}
	}
}

func (m *gpioInputMonitor) publish(status gpioInputStatus, err error) {
	m.mu.Lock()
	m.status = status
	m.err = err
	m.mu.Unlock()
	m.readyOnce.Do(func() { close(m.ready) })
}

func (m *gpioInputMonitor) waitRetry() bool {
	timer := time.NewTimer(m.retryInterval)
	defer timer.Stop()
	select {
	case <-m.stop:
		return false
	case <-timer.C:
		return true
	}
}

func closeLine(line gpioio.Line) error {
	if line == nil {
		return nil
	}
	return line.Close()
}
