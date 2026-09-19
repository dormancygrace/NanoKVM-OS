package main

import (
	"NanoKVM-Server/osupdate"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

func main() {
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "root required")
		os.Exit(1)
	}
	if len(os.Args) == 2 && os.Args[1] == "self-test" {
		sequence, _ := strconv.ParseUint(osupdate.UpdaterBuildSequence, 10, 64)
		_ = json.NewEncoder(os.Stdout).Encode(osupdate.UpdaterSelfTest{Protocol: 1, Capability: osupdate.UpdaterCapability, BuildVersion: osupdate.UpdaterBuildVersion, BuildSequence: sequence})
		return
	}
	var lock *os.File
	var err error
	if len(os.Args) == 3 && (os.Args[1] == "install-inherited" || os.Args[1] == "prepare-inherited" || os.Args[1] == "apk-inherited") {
		lock = os.NewFile(3, "update-lock")
		if lock == nil {
			os.Exit(1)
		}
		canonical, statErr := os.Lstat(osupdate.Base + "/lock")
		inherited, inheritedErr := lock.Stat()
		if statErr != nil || inheritedErr != nil || !canonical.Mode().IsRegular() || !os.SameFile(canonical, inherited) {
			fmt.Fprintln(os.Stderr, "invalid inherited update lock")
			os.Exit(1)
		}
		// Keep ownership in the helper, never in services it launches.
		syscall.CloseOnExec(int(lock.Fd()))
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	} else {
		lock, err = osupdate.Lock()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer lock.Close()
	if len(os.Args) == 3 && os.Args[1] == "apk-inherited" {
		if err = osupdate.RunAPK(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err = osupdate.RecoverUpdater(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(os.Args) == 3 && os.Args[1] == "prepare-inherited" {
		var bundle *osupdate.Bundle
		bundle, err = osupdate.Prepare(os.Args[2])
		if err == nil {
			err = json.NewEncoder(os.Stdout).Encode(osupdate.Receipt(bundle))
		}
	} else if len(os.Args) == 2 && os.Args[1] == "system-boot" {
		err = osupdate.SystemBoot()
	} else if len(os.Args) == 2 && os.Args[1] == "system-confirm" {
		err = osupdate.ConfirmSystem()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			if !osupdate.HasFullUpdate() {
				_ = exec.Command("/sbin/reboot").Run()
			}
		}
	} else if len(os.Args) == 2 && os.Args[1] == "recover" {
		err = osupdate.Recover()
	} else if len(os.Args) == 3 && (os.Args[1] == "install" || os.Args[1] == "install-inherited") {
		err = osupdate.Install(os.Args[2], lock)
	} else {
		err = fmt.Errorf("usage: nkos-update self-test | prepare-inherited FILE | recover | system-boot | system-confirm | install PACKAGE_ID")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
