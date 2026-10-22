package vm

import (
	"os/exec"

	"NanoKVM-Server/internal/oomscore"
)

// The shell terminal on the NanoKVM itself. A serial terminal (port=...)
// is a direct bridge to the device, see serial_session.go.
func terminalCommand() *exec.Cmd {
	argv := oomscore.Unprotected("/bin/sh", "-l")
	return exec.Command(argv[0], argv[1:]...)
}
