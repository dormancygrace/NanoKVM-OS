package main

import (
	"NanoKVM-Server/osupdate"
	"fmt"
	"os"
	"syscall"
)

func main() {
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "root required")
		os.Exit(1)
	}
	var lock *os.File
	var err error
	if len(os.Args) == 3 && os.Args[1] == "install-inherited" {
		lock = os.NewFile(3, "update-lock")
		if lock == nil {
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
	if len(os.Args) == 2 && os.Args[1] == "recover" {
		err = osupdate.Recover()
	} else if len(os.Args) == 3 && (os.Args[1] == "install" || os.Args[1] == "install-inherited") {
		err = osupdate.Install(os.Args[2])
	} else {
		err = fmt.Errorf("usage: nkos-update recover | install PACKAGE_ID")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
