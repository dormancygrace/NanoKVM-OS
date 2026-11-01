package common

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A crash prints every goroutine to descriptor 2 after the runtime has
// stopped the goroutine that reads the capture pipe. The process must still
// exit (so that the supervisor restarts it), not block on the full pipe:
// the test fails if the child has not exited within 20 s.
func TestCrashWithCapturedStderrExits(t *testing.T) {
	if reportPath := os.Getenv("NANOKVM_STDERR_CRASH_CHILD"); reportPath != "" {
		report, err := os.Create(reportPath)
		if err != nil {
			os.Exit(3)
		}
		if err := debug.SetCrashOutput(report, debug.CrashOptions{}); err != nil {
			os.Exit(3)
		}
		_ = report.Close() // SetCrashOutput owns a duplicate descriptor.
		if err := CaptureNativeStderr(io.Discard); err != nil {
			os.Exit(3)
		}
		debug.SetTraceback("all")
		block := make(chan struct{})
		ready := make(chan struct{}, 400)
		var deep func(int)
		deep = func(n int) {
			if n == 0 {
				ready <- struct{}{}
				<-block
				return
			}
			deep(n - 1)
		}
		// Well over the 64 KiB a pipe holds once every goroutine is printed.
		for i := 0; i < 400; i++ {
			go deep(8)
		}
		for i := 0; i < 400; i++ {
			<-ready
		}
		panic("crash with stderr captured")
	}
	reportPath := filepath.Join(t.TempDir(), "crash.txt")
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashWithCapturedStderrExits$")
	cmd.Env = append(os.Environ(), "NANOKVM_STDERR_CRASH_CHILD="+reportPath, "GOTRACEBACK=all")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 2 {
			t.Fatalf("child exit: %v, want panic exit status 2", err)
		}
		report, err := os.ReadFile(reportPath)
		if err != nil {
			t.Fatal(err)
		}
		if len(report) <= 64<<10 || !bytes.Contains(report, []byte("panic: crash with stderr captured")) || bytes.Count(report, []byte("\ngoroutine ")) < 400 {
			t.Fatalf("incomplete crash report: %d bytes, %d goroutines", len(report), bytes.Count(report, []byte("\ngoroutine ")))
		}
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		t.Fatal("the crashing process hung on the full stderr pipe")
	}
}

// A process that execs itself must not leave the capture pipe on descriptor
// 2: its read end closes on exec, and the new image dies of SIGPIPE at its
// first line on stderr. The child captures stderr, optionally restores it,
// and execs a shell that writes to stderr.
func TestRestoreNativeStderrBeforeExec(t *testing.T) {
	if mode := os.Getenv("NANOKVM_STDERR_EXEC_CHILD"); mode != "" {
		if err := CaptureNativeStderr(io.Discard); err != nil {
			os.Exit(3)
		}
		if mode == "restore" {
			RestoreNativeStderr()
		}
		_ = syscall.Exec("/bin/sh", []string{"sh", "-c", "echo started >&2"}, os.Environ())
		os.Exit(4)
	}
	run := func(mode string) (string, error) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestRestoreNativeStderrBeforeExec$")
		cmd.Env = append(os.Environ(), "NANOKVM_STDERR_EXEC_CHILD="+mode)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		return stderr.String(), err
	}
	if out, err := run("restore"); err != nil || !strings.Contains(out, "started") {
		t.Fatalf("restored: %v, stderr %q", err, out)
	}
	_, err := run("keep")
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("without restoring: %v, want SIGPIPE", err)
	}
	if status, ok := exit.Sys().(syscall.WaitStatus); !ok || !status.Signaled() || status.Signal() != syscall.SIGPIPE {
		t.Fatalf("without restoring: %v, want SIGPIPE", err)
	}
}
