// gpio-atx-probe exercises ONLY the disconnected ATX outputs on a PCIe board.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"NanoKVM-Server/internal/gpioio"
)

type observed struct {
	gpioio.Line
	offset   uint32
	started  time.Time
	cancel   context.CancelFunc
	released bool
	closed   bool
}

func (l *observed) Close() error { err := l.Line.Close(); l.closed = err == nil; return err }

func (l *observed) Set(value bool) error {
	if err := l.Line.Set(value); err != nil {
		return err
	}
	got, err := l.Line.Get()
	if err != nil {
		return err
	}
	if got != value {
		return fmt.Errorf("GPIOA%d readback %t expected %t", l.offset, got, value)
	}
	if value {
		l.started = time.Now()
		if l.cancel != nil {
			l.cancel()
		}
	}
	if !value {
		l.released = true
	}
	fmt.Printf("GPIOA%d high=%t elapsed=%s\n", l.offset, got, time.Since(l.started))
	return nil
}
func main() {
	confirmed := flag.Bool("confirm-disconnected", false, "ATX wires are disconnected and legacy exports were released")
	flag.Parse()
	if !*confirmed {
		fmt.Fprintln(os.Stderr, "explicit disconnected-ATX confirmation required")
		os.Exit(2)
	}
	// Bound this diagnostic process; the caller restores the legacy exports.
	deadline := time.AfterFunc(5*time.Second, func() { fmt.Fprintln(os.Stderr, "GPIO diagnostic timed out"); os.Exit(124) })
	defer deadline.Stop()
	var controller gpioio.Controller
	for _, offset := range []uint32{23, 25} {
		err := controller.Pulse(context.Background(), 50*time.Millisecond, func() (gpioio.Line, error) {
			line, err := gpioio.Open(gpioio.Path("3020000.gpio", offset), true)
			if err != nil {
				return nil, err
			}
			low, err := line.Get()
			if err != nil || low {
				line.Close()
				return nil, fmt.Errorf("initial level not inactive: %v high=%t", err, low)
			}
			return &observed{Line: line, offset: offset}, nil
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var watched *observed
	err := controller.Pulse(ctx, time.Second, func() (gpioio.Line, error) {
		line, err := gpioio.Open(gpioio.Path("3020000.gpio", 23), true)
		if err != nil {
			return nil, err
		}
		watched = &observed{Line: line, offset: 23, cancel: cancel}
		return watched, nil
	})
	if !errors.Is(err, context.Canceled) || watched == nil || !watched.released || !watched.closed {
		fmt.Fprintln(os.Stderr, "expected cancellation:", err)
		os.Exit(1)
	}
	fmt.Println("PASS GPIO-v2 inactive request, two 50ms ATX pulses and cancellation release with output readback")
}
