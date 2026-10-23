package common

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"
)

// The native video libraries report failures on stderr (encoder teardown,
// a frame that does not complete, rejected packs), and the service start
// discards it. CaptureNativeStderr routes file descriptor 2 into w, the
// application log. The reader never blocks the native code: a full queue
// drops lines, and at most nativeLinesPerSecond reach the log.
const (
	nativeLinesPerSecond = 20
	nativeQueueLines     = 256
	nativeLineBytes      = 4096 // longer lines are cut, the rest is read and dropped
)

// originalStderr is descriptor 2 as the service started it (close-on-exec),
// or -1 before CaptureNativeStderr.
var originalStderr = -1

func CaptureNativeStderr(w io.Writer) error {
	r, pipe, err := os.Pipe()
	if err != nil {
		return err
	}
	original, err := syscall.Dup(2)
	if err != nil {
		_ = r.Close()
		_ = pipe.Close()
		return err
	}
	syscall.CloseOnExec(original)
	if err := syscall.Dup3(int(pipe.Fd()), 2, 0); err != nil {
		_ = syscall.Close(original)
		_ = r.Close()
		_ = pipe.Close()
		return err
	}
	// A full pipe must drop writes, not block them: the reader is a
	// goroutine, and a crashing runtime stops every goroutine before it
	// writes its traceback to descriptor 2. A blocking write there would
	// hang the process instead of letting it exit and be restarted.
	// (pipe.Fd above made the descriptor blocking.)
	if err := syscall.SetNonblock(2, true); err != nil {
		_ = syscall.Dup3(original, 2, 0)
		_ = syscall.Close(original)
		_ = r.Close()
		_ = pipe.Close()
		return err
	}
	originalStderr = original
	// Descriptor 2 now holds the pipe; this copy is not needed.
	_ = pipe.Close()
	lines := make(chan string, nativeQueueLines)
	go func() {
		readNativeStderr(r, lines)
		// The pipe has no reader any more: writes to it would block the
		// native code, so descriptor 2 goes back to the service's stderr.
		RestoreNativeStderr()
	}()
	go writeNativeStderr(lines, w, time.Now)
	return nil
}

// readNativeStderr queues the lines of r until it ends or fails. It never
// stops on its own: a line longer than nativeLineBytes is cut (and marked),
// the rest of it is read and dropped, and reading goes on.
func readNativeStderr(r io.Reader, lines chan<- string) {
	defer close(lines)
	reader := bufio.NewReaderSize(r, nativeLineBytes)
	var line []byte
	cut := false
	for {
		chunk, err := reader.ReadSlice('\n')
		if room := nativeLineBytes - len(line); len(chunk) > room {
			line, cut = append(line, chunk[:room]...), true
		} else {
			line = append(line, chunk...)
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if len(line) > 0 {
			text := strings.TrimRight(string(line), "\r\n")
			if cut {
				text += " [cut]"
			}
			select {
			case lines <- text:
			default: // the log is behind: drop rather than block a video thread
			}
		}
		line, cut = line[:0], false
		if err != nil {
			return
		}
	}
}

func writeNativeStderr(lines <-chan string, w io.Writer, now func() time.Time) {
	var window time.Time
	written, dropped := 0, 0
	for line := range lines {
		t := now()
		if t.Sub(window) >= time.Second {
			if dropped > 0 {
				_, _ = fmt.Fprintf(w, "[%s] [native] %d more lines dropped\n", t.Format("2006-01-02 15:04:05.000"), dropped)
			}
			window, written, dropped = t, 0, 0
		}
		if written >= nativeLinesPerSecond {
			dropped++
			continue
		}
		written++
		_, _ = fmt.Fprintf(w, "[%s] [native] %s\n", t.Format("2006-01-02 15:04:05.000"), line)
	}
}

// RestoreNativeStderr puts the service's stderr back on descriptor 2. An
// exec must not inherit the capture pipe: its read end closes on exec, so
// the next write to stderr would kill the new image with SIGPIPE.
func RestoreNativeStderr() {
	if originalStderr >= 0 {
		_ = syscall.Dup3(originalStderr, 2, 0)
	}
}
