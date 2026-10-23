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

func TestNativeStderrRateLimit(t *testing.T) {
	lines := make(chan string, 100)
	for i := 0; i < 30; i++ {
		lines <- "[kvm_mmf] frame still pending"
	}
	lines <- "next second"
	close(lines)

	start := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	calls := 0
	now := func() time.Time {
		calls++
		if calls > 30 {
			return start.Add(time.Second)
		}
		return start
	}
	var out bytes.Buffer
	writeNativeStderr(lines, &out, now)

	got := out.String()
	if n := strings.Count(got, "frame still pending"); n != nativeLinesPerSecond {
		t.Fatalf("%d lines in the first second, want %d:\n%s", n, nativeLinesPerSecond, got)
	}
	if !strings.Contains(got, "[native] 10 more lines dropped") || !strings.Contains(got, "[native] next second") {
		t.Fatalf("missing drop summary or later line:\n%s", got)
	}
}

func TestNativeStderrReaderNeverBlocks(t *testing.T) {
	lines := make(chan string, 2)
	done := make(chan struct{})
	go func() {
		readNativeStderr(strings.NewReader("a\nb\nc\nd\n"), lines)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reader blocked on a full queue")
	}
	if got := []string{<-lines, <-lines}; got[0] != "a" || got[1] != "b" {
		t.Fatalf("queued %q", got)
	}
}

func TestNativeStderrReaderSurvivesLongLines(t *testing.T) {
	long := strings.Repeat("x", 70<<10)
	lines := make(chan string, 8)
	readNativeStderr(strings.NewReader(long+"\nAFTER\nlast without newline"), lines)
	var got []string
	for line := range lines {
		got = append(got, line)
	}
	if len(got) != 3 || got[1] != "AFTER" || got[2] != "last without newline" {
		t.Fatalf("lines %d: %.40q", len(got), got)
	}
	if !strings.HasSuffix(got[0], " [cut]") || len(got[0]) != nativeLineBytes+len(" [cut]") {
		t.Fatalf("long line not cut: %d bytes", len(got[0]))
	}
}

type failingReader struct{ data string }

func (r *failingReader) Read(p []byte) (int, error) {
	if r.data == "" {
		return 0, errors.New("read failed")
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

func TestNativeStderrReaderStopsOnReadError(t *testing.T) {
	lines := make(chan string, 8)
	readNativeStderr(&failingReader{data: "a\npartial"}, lines)
	var got []string
	for line := range lines {
		got = append(got, line)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "partial" {
		t.Fatalf("got %q", got)
	}
}

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
