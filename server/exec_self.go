package main

import (
	"os"
	"syscall"

	"NanoKVM-Server/common"
)

// execSelf replaces the process with a fresh start of the server binary. Every
// in-place restart goes through it: the native stderr capture leaves
// descriptor 2 on a pipe whose read end closes on exec, so the new image would
// die of SIGPIPE at its first log line; the service's stderr goes back first.
func execSelf(env []string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	common.RestoreNativeStderr()
	return syscall.Exec(executable, os.Args, env)
}
