package standby

import (
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"time"
)

// RecoveryError is produced only after complete persistent-mount release and
// disk/partition protection. Earlier failures must not imply a safe recovery.
type RecoveryError struct{ Cause error }

func (e *RecoveryError) Error() string {
	return fmt.Sprintf("RAM service failed after storage release; keep power connected for automatic restart in two minutes: %v", e.Cause)
}
func (e *RecoveryError) Unwrap() error { return e.Cause }

// RecoverFatal is used only by the new PID 1. A broken RAM listener must not
// strand a PCIe unit after storage has already been released successfully.
func RecoverFatal(err error) error {
	return recoverFatal(err, time.Sleep, func() error { return unix.Reboot(unix.LINUX_REBOOT_CMD_RESTART) })
}
func recoverFatal(err error, wait func(time.Duration), reboot func() error) error {
	var safe *RecoveryError
	if !errors.As(err, &safe) {
		return err
	}
	wait(2 * time.Minute)
	if e := reboot(); e != nil {
		return fmt.Errorf("%w; automatic restart failed: %v", err, e)
	}
	return nil
}
