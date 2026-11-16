package main

import (
	"fmt"
	"os"

	"NanoKVM-Server/osupdate"
	"NanoKVM-Server/recovery"
)

// factoryReset resets the settings and reboots, as Settings > Factory reset
// does without "Keep users" and with "Keep apps": NanoKVM-Server
// factory-reset, which kvm_system runs when the BOOT button is held for 9
// seconds. Web access returns to admin/admin, root's password is locked,
// SSH keys go and SSH is off; installed software stays, its settings go.
func factoryReset(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "usage: NanoKVM-Server factory-reset")
		fmt.Fprintln(os.Stderr, "Resets the settings (installed software stays) and reboots.")
		return 2
	}
	if !recovery.Available() {
		fmt.Fprintln(os.Stderr, "factory-reset: update nanokvm-base before resetting settings")
		return 1
	}
	if err := stageFactoryReset(); err != nil {
		fmt.Fprintf(os.Stderr, "factory-reset: %v\n", err)
		return 1
	}
	if err := recovery.Reboot(); err != nil {
		fmt.Fprintf(os.Stderr, "factory-reset: reboot: %v\n", err)
		return 1
	}
	return 0
}

func stageFactoryReset() error {
	lock, err := osupdate.Lock()
	if err != nil {
		return err
	}
	defer lock.Close()
	e := recovery.Live()
	plan, _, err := e.Prepare(nil, recovery.Options{Action: "reset", KeepUsers: false, KeepApps: true})
	if err != nil {
		return err
	}
	return e.Stage(plan)
}
