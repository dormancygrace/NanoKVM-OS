package picoclaw

import (
	"errors"
	"os"
	"syscall"
	"testing"
)

func TestGatewayProcessExitsDuringCmdlineRead(t *testing.T) {
	previous := readPicoclawProcessCmdline
	t.Cleanup(func() { readPicoclawProcessCmdline = previous })
	for _, tc := range []struct {
		name      string
		err       error
		wantError bool
	}{
		{"removed before open", os.ErrNotExist, false},
		{"exited during read", syscall.ESRCH, false},
		{"access denied", os.ErrPermission, true},
		{"read failure", syscall.EIO, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			readPicoclawProcessCmdline = func(path string) ([]byte, error) {
				if path != "/proc/12345/cmdline" {
					t.Fatalf("unexpected process path: %s", path)
				}
				return nil, &os.PathError{Op: "read", Path: path, Err: tc.err}
			}
			running, err := isPicoclawGatewayProcess(12345)
			if running || (err != nil) != tc.wantError {
				t.Fatalf("running=%v, error=%v; want running=false, error=%v", running, err, tc.wantError)
			}
			if tc.wantError && !errors.Is(err, tc.err) {
				t.Fatalf("lost original error: %v", err)
			}
		})
	}
}
