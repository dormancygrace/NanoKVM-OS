package common

import (
	"bufio"
	"fmt"
	"io"
	"os"
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
)

func CaptureNativeStderr(w io.Writer) error {
	r, pipe, err := os.Pipe()
	if err != nil {
		return err
	}
	if err := syscall.Dup3(int(pipe.Fd()), 2, 0); err != nil {
		r.Close()
		pipe.Close()
		return err
	}
	pipe.Close()
	lines := make(chan string, nativeQueueLines)
	go readNativeStderr(r, lines)
	go writeNativeStderr(lines, w, time.Now)
	return nil
}

func readNativeStderr(r io.Reader, lines chan<- string) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 64<<10)
	for scanner.Scan() {
		select {
		case lines <- scanner.Text():
		default: // the log is behind: drop rather than block a video thread
		}
	}
	close(lines)
}

func writeNativeStderr(lines <-chan string, w io.Writer, now func() time.Time) {
	var window time.Time
	written, dropped := 0, 0
	for line := range lines {
		t := now()
		if t.Sub(window) >= time.Second {
			if dropped > 0 {
				fmt.Fprintf(w, "[%s] [native] %d more lines dropped\n", t.Format("2006-01-02 15:04:05.000"), dropped)
			}
			window, written, dropped = t, 0, 0
		}
		if written >= nativeLinesPerSecond {
			dropped++
			continue
		}
		written++
		fmt.Fprintf(w, "[%s] [native] %s\n", t.Format("2006-01-02 15:04:05.000"), line)
	}
}
