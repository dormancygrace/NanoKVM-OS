package vm

import (
	"context"
	"errors"
	"sync"
	"time"

	"NanoKVM-Server/config"
	"NanoKVM-Server/internal/gpioio"
)

const (
	gpioInputSampleInterval = 20 * time.Millisecond
	hddActivityHold         = 350 * time.Millisecond
	gpioInputRetryInterval  = time.Second
)

type gpioInputStatus struct {
	Power bool
	HDD   bool
}

type gpioInputPaths func() (power, hdd string)
type gpioLineOpen func(device string, output bool) (gpioio.Line, error)

type gpioInputMonitor struct {
	paths gpioInputPaths
	open  gpioLineOpen

	sampleInterval time.Duration
	hddHold        time.Duration
	retryInterval  time.Duration

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
)

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
		power, hdd, err := m.openInputs()
		if err != nil {
			m.publish(gpioInputStatus{}, err)
			if !m.waitRetry() {
				return
			}
			continue
		}

		retry := m.sampleInputs(power, hdd)
		err = errors.Join(power.Close(), closeLine(hdd))
		if err != nil {
			m.publish(gpioInputStatus{}, err)
		}
		if !retry || !m.waitRetry() {
			return
		}
	}
}

func (m *gpioInputMonitor) openInputs() (gpioio.Line, gpioio.Line, error) {
	powerPath, hddPath := m.paths()
	power, err := m.open(powerPath, false)
	if err != nil {
		return nil, nil, err
	}
	if hddPath == "" {
		return power, nil, nil
	}
	hdd, err := m.open(hddPath, false)
	if err != nil {
		return nil, nil, errors.Join(err, power.Close())
	}
	return power, hdd, nil
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
