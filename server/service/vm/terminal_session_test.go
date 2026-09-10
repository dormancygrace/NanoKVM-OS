package vm

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

func terminalTestSession(t *testing.T, script string) (*websocket.Conn, <-chan error) {
	t.Helper()
	return terminalTestCommand(t, exec.Command("/bin/sh", "-c", script))
}

func terminalTestCommand(t *testing.T, cmd *exec.Cmd) (*websocket.Conn, <-chan error) {
	t.Helper()
	done := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrade := websocket.Upgrader{}
		ws, err := upgrade.Upgrade(w, r, nil)
		if err != nil {
			done <- err
			return
		}
		defer ws.Close()
		err = runTerminalSession(ws, cmd)
		if err == nil && cmd.ProcessState == nil {
			t.Error("child was not reaped")
		}
		done <- err
	}))
	t.Cleanup(server.Close)
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close() })
	return ws, done
}

func terminalReadUntil(t *testing.T, ws *websocket.Conn, marker string) string {
	t.Helper()
	ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	var output strings.Builder
	for !strings.Contains(output.String(), marker) {
		kind, data, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for %q: %v; output=%q", marker, err, output.String())
		}
		if kind != websocket.BinaryMessage {
			t.Fatalf("unexpected kind %d", kind)
		}
		output.Write(data)
	}
	return output.String()
}

func terminalWaitDone(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("terminal session did not finish")
	}
}

func TestTerminalANSIResizeAndShellExit(t *testing.T) {
	ws, done := terminalTestSession(t, `printf 'TERM=%s\n' "$TERM"; stty size; printf '\033[31mCOLOR\033[0m\nREADY\n'; while IFS= read -r line; do case "$line" in size) stty size;; exit) exit;; esac; done`)
	output := terminalReadUntil(t, ws, "READY")
	for _, expected := range []string{"TERM=xterm-256color", "24 80", "\x1b[31mCOLOR\x1b[0m"} {
		if !strings.Contains(output, expected) {
			t.Errorf("missing %q in %q", expected, output)
		}
	}
	if err := ws.WriteMessage(websocket.BinaryMessage, []byte(`{"rows":40,"cols":120}`)); err != nil {
		t.Fatal(err)
	}
	if err := ws.WriteMessage(websocket.TextMessage, []byte("size\n")); err != nil {
		t.Fatal(err)
	}
	terminalReadUntil(t, ws, "40 120")
	for _, invalid := range []string{`{"rows":0,"cols":0}`, `{"rows":-1,"cols":80}`, `not json`} {
		if err := ws.WriteMessage(websocket.BinaryMessage, []byte(invalid)); err != nil {
			t.Fatal(err)
		}
	}
	if err := ws.WriteMessage(websocket.TextMessage, []byte("size\n")); err != nil {
		t.Fatal(err)
	}
	terminalReadUntil(t, ws, "40 120")
	if err := ws.WriteMessage(websocket.TextMessage, []byte("exit\n")); err != nil {
		t.Fatal(err)
	}
	terminalWaitDone(t, done)
	ws.SetReadDeadline(time.Now().Add(time.Second))
	for {
		_, _, err := ws.ReadMessage()
		if err != nil {
			break
		}
	}
}

func TestTerminalClientCloseStopsIdlePTY(t *testing.T) {
	ws, done := terminalTestSession(t, `printf 'READY\n'; read -r line`)
	terminalReadUntil(t, ws, "READY")
	ws.Close()
	terminalWaitDone(t, done)
}

func TestTerminalPicocomANSIAndInput(t *testing.T) {
	picocom, err := exec.LookPath("picocom")
	if err != nil {
		t.Skip("picocom unavailable on test host")
	}
	remote, serial, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	defer serial.Close()
	if err := syscall.SetNonblock(int(remote.Fd()), true); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(picocom, serial.Name(), "--baud", "115200", "--parity", "none", "--flow", "none", "--databits", "8", "--stopbits", "1", "--imap", "lfcrlf", "--noreset")
	ws, done := terminalTestCommand(t, cmd)
	terminalReadUntil(t, ws, "Terminal ready")
	const ansi = "\x1b[32mПривет COLOR\x1b[0m"
	if _, err := remote.Write([]byte(ansi)); err != nil {
		t.Fatal(err)
	}
	terminalReadUntil(t, ws, ansi)
	if err := ws.WriteMessage(websocket.TextMessage, []byte("serial-input\r")); err != nil {
		t.Fatal(err)
	}
	if err := remote.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("serial-input\r"))
	if _, err := io.ReadFull(remote, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "serial-input\r" {
		t.Fatalf("serial input=%q", got)
	}
	if err := ws.WriteMessage(websocket.TextMessage, []byte{1, 24}); err != nil {
		t.Fatal(err)
	}
	terminalWaitDone(t, done)
}
