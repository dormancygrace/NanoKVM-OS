package main

import (
	"fmt"
	"os"
	"time"

	"NanoKVM-Server/standby"
)

func main() {
	var err error
	if len(os.Args) < 2 {
		err = fmt.Errorf("usage: nkos-standby {probe ROOT|acknowledge|shutdown|takeover}")
	} else {
		switch os.Args[1] {
		case "probe":
			if len(os.Args) != 3 {
				err = fmt.Errorf("probe requires RAM root")
			} else {
				err = standby.Probe(os.Args[2])
			}
		case "acknowledge":
			err = standby.Acknowledge()
		case "shutdown":
			err = standby.Shutdown()
		case "takeover":
			err = standby.Takeover()
		case "bound", "renew", "deconfig", "nak", "leasefail":
			err = standby.DHCPHook(os.Args[1])
		default:
			err = fmt.Errorf("unknown operation")
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	// Exiting PID1 panics the kernel; keep console evidence on fatal setup
	// failure. Never claim safe or silently reboot.
	if os.Getpid() == 1 {
		for {
			time.Sleep(time.Hour)
		}
	}
	if err != nil {
		os.Exit(1)
	}
}
