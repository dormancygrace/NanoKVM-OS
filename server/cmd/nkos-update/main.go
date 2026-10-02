// nkos-update runs native APK operations detached from NanoKVM-Server, so
// upgrading or restarting the web server cannot interrupt a transaction.
package main

import (
	"NanoKVM-Server/osupdate"
	"fmt"
	"os"
	"syscall"
)

const usage = "usage: nkos-update apk-inherited ACTION | software-inherited ACTION PACKAGE"

func main() {
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "root required")
		os.Exit(1)
	}
	apk := len(os.Args) == 3 && os.Args[1] == "apk-inherited"
	software := len(os.Args) == 4 && os.Args[1] == "software-inherited"
	if !apk && !software {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}
	lock := os.NewFile(3, "update-lock")
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
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer lock.Close()
	var err error
	if apk {
		err = osupdate.RunAPK(os.Args[2])
	} else {
		err = osupdate.RunSoftware(os.Args[2], os.Args[3])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
