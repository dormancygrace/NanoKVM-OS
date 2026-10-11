package vm

import "os/exec"

// The shell terminal on the NanoKVM itself. A serial terminal (port=...)
// is a direct bridge to the device, see serial_session.go.
func terminalCommand() *exec.Cmd {
	return exec.Command("/bin/sh", "-l")
}
