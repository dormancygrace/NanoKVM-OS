// nkos-usb-internet is called only by the existing serialized S30usbnet
// lifecycle. The OpenRC watcher reapplies that lifecycle without rebinding USB.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"NanoKVM-Server/usbinternet"
)

func main() {
	r := usbinternet.DefaultRuntime()
	if len(os.Args) < 2 {
		fail(fmt.Errorf("usage: nkos-usb-internet {apply PREFIX|stop|watch|watch-needed}"))
	}
	switch os.Args[1] {
	case "watch-needed":
		if !r.WatchNeeded() {
			os.Exit(1)
		}
	case "apply":
		if len(os.Args) != 3 {
			fail(fmt.Errorf("USB prefix required"))
		}
		fail(r.Apply(os.Args[2], false))
	case "stop":
		fail(r.Apply("", true))
	case "watch":
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		var monitors sync.WaitGroup
		defer func() {
			cancel()
			// Reap ip/nft before the watcher exits. Otherwise a monitor can
			// survive cancellation because its goroutine dies with main.
			monitors.Wait()
		}()
		events := make(chan struct{}, 1)
		// Refresh cached routes/flows promptly on link, routing and firewall
		// changes. The timer also covers missed events or failed monitors.
		monitor(ctx, events, &monitors, "ip", "monitor", "link", "address", "route", "rule")
		monitor(ctx, events, &monitors, "nft", "monitor")
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		script := os.Getenv("NANOKVM_USB_NET_SCRIPT")
		if script == "" {
			script = "/etc/init.d/S30usbnet"
		}
		for {
			commandCtx, done := context.WithTimeout(ctx, 15*time.Second)
			cmd := exec.CommandContext(commandCtx, "sh", script, "start")
			// Lifecycle entry points manage this supervisor. A reconciliation
			// invoked by the watcher must never synchronously stop itself.
			cmd.Env = append(os.Environ(), "NANOKVM_USB_NET_WATCHER=1")
			if err := cmd.Run(); err != nil && ctx.Err() == nil {
				fmt.Fprintln(os.Stderr, "USB network reconciliation failed")
			}
			done()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-events:
			}
		}
	default:
		fail(fmt.Errorf("unknown action"))
	}
}
func fail(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func monitor(ctx context.Context, events chan<- struct{}, group *sync.WaitGroup, name string, args ...string) {
	group.Add(1)
	go func() {
		defer group.Done()
		cmd := exec.CommandContext(ctx, name, args...)
		pipe, err := cmd.StdoutPipe()
		if err != nil {
			return
		}
		if err = cmd.Start(); err != nil {
			return
		}
		scanner := bufio.NewScanner(pipe)
		for scanner.Scan() {
			if name == "nft" && strings.Contains(scanner.Text(), usbinternet.Table) {
				continue
			}
			select {
			case events <- struct{}{}:
			default:
			}
		}
		_ = cmd.Wait()
	}()
}
