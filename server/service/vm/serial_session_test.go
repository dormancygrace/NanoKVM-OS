package vm

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"golang.org/x/sys/unix"
)

// serialTestDevice is a pty: the slave stands in for the serial device, the
// master is the computer at the other end of the cable.
func serialTestDevice(t *testing.T) (*os.File, string) {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	name := slave.Name()
	// The session opens the device by name; the test keeps only the master.
	slave.Close()
	t.Cleanup(func() { master.Close() })
	if err := syscall.SetNonblock(int(master.Fd()), true); err != nil {
		t.Fatal(err)
	}
	return master, name
}

func serialTestSession(t *testing.T, s *serialSettings) (*websocket.Conn, <-chan error) {
	t.Helper()
	done := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			done <- err
			return
		}
		defer ws.Close()
		done <- runSerialSession(ws, s)
	}))
	t.Cleanup(server.Close)
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close() })
	return ws, done
}

func readMaster(t *testing.T, master *os.File, want int) []byte {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var got []byte
	buf := make([]byte, 4096)
	for len(got) < want && time.Now().Before(deadline) {
		master.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		n, _ := master.Read(buf)
		got = append(got, buf[:n]...)
	}
	return got
}

func waitRaw(t *testing.T, master *os.File) {
	t.Helper()
	// the session has opened the slave once its termios are raw
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		attr, err := unix.IoctlGetTermios(int(master.Fd()), unix.TCGETS)
		if err == nil && attr.Lflag&unix.ICANON == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the session did not set the line raw")
}

func TestSerialSessionBridgesBothWays(t *testing.T) {
	master, device := serialTestDevice(t)
	ws, done := serialTestSession(t, &serialSettings{Device: device, Baud: 9600, Parity: "none", Flow: "none", DataBits: 8, StopBits: 1})
	waitRaw(t, master)

	const ansi = "\x1b[32mПривет COLOR\x1b[0m"
	if _, err := master.Write([]byte(ansi)); err != nil {
		t.Fatal(err)
	}
	if got := terminalReadUntil(t, ws, ansi); got != ansi {
		t.Fatalf("output %q", got)
	}
	// browser input as text, resize JSON dropped, the OSC 777 reply is input
	if err := ws.WriteMessage(websocket.BinaryMessage, []byte(`{"rows":40,"cols":120}`)); err != nil {
		t.Fatal(err)
	}
	reply := "\x1b]777;nkos-size;1;" + strings.Repeat("a", 32) + ";40;120\x07"
	if err := ws.WriteMessage(websocket.TextMessage, []byte("serial-input\r"+reply)); err != nil {
		t.Fatal(err)
	}
	want := "serial-input\r" + reply
	if got := readMaster(t, master, len(want)); string(got) != want {
		t.Fatalf("the device received %q", got)
	}
	// binary passthrough from the device
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	if _, err := master.Write(all); err != nil {
		t.Fatal(err)
	}
	var got []byte
	ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	for len(got) < 256 {
		_, data, err := ws.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, data...)
	}
	if !bytes.Equal(got, all) {
		t.Fatalf("binary output changed: %x", got)
	}
	ws.Close()
	terminalWaitDone(t, done)
	if serialPortOpen(device) {
		t.Fatal("the port stayed claimed after the session")
	}
}

func TestSerialSessionAppliesLineSettings(t *testing.T) {
	master, device := serialTestDevice(t)
	ws, done := serialTestSession(t, &serialSettings{Device: device, Baud: 115200, Parity: "even", Flow: "none", DataBits: 7, StopBits: 2})
	waitRaw(t, master)
	attr, err := unix.IoctlGetTermios(int(master.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	// The pty driver forces CS8 without parity (pty_set_termios), so those
	// two are covered by TestSerialTermios; the rest reaches the device.
	if attr.Cflag&unix.CSTOPB == 0 || attr.Cflag&unix.CBAUD != unix.B115200 || attr.Oflag&unix.OPOST != 0 ||
		attr.Cflag&unix.CLOCAL == 0 || attr.Lflag&(unix.ICANON|unix.ECHO|unix.ISIG) != 0 {
		t.Fatalf("line settings on the device: %+v", attr)
	}
	ws.Close()
	terminalWaitDone(t, done)
}

func TestSerialSessionPasteArrivesInOrder(t *testing.T) {
	master, device := serialTestDevice(t)
	ws, done := serialTestSession(t, &serialSettings{Device: device, Baud: 9600, Parity: "none", Flow: "none", DataBits: 8, StopBits: 1})
	waitRaw(t, master)
	paste := bytes.Repeat([]byte("0123456789abcdefghijklmnopqrstuvwxyz\r"), 400) // 14.8 KiB
	received := make(chan []byte, 1)
	go func() { received <- readMaster(t, master, len(paste)) }()
	for i := 0; i < len(paste); i += 4096 {
		end := min(i+4096, len(paste))
		if err := ws.WriteMessage(websocket.TextMessage, paste[i:end]); err != nil {
			t.Fatal(err)
		}
	}
	if got := <-received; !bytes.Equal(got, paste) {
		t.Fatalf("paste changed: %d of %d bytes", len(got), len(paste))
	}
	ws.Close()
	terminalWaitDone(t, done)
}

func TestSerialSessionIsExclusiveAndSeenByTheProbe(t *testing.T) {
	serialLockDir = t.TempDir()
	master, device := serialTestDevice(t)
	settings := &serialSettings{Device: device, Baud: 9600, Parity: "none", Flow: "none", DataBits: 8, StopBits: 1}
	ws, done := serialTestSession(t, settings)
	waitRaw(t, master)
	if r := probeSerialConsole(device, time.Second); r.State != "busy" {
		t.Fatalf("probe while the terminal is open: %+v", r)
	}
	second, secondDone := serialTestSession(t, settings)
	if got := terminalReadUntil(t, second, "in use"); !strings.Contains(got, "another terminal session") {
		t.Fatalf("second session: %q", got)
	}
	terminalWaitDone(t, secondDone)
	// the first still works
	if _, err := master.Write([]byte("still here")); err != nil {
		t.Fatal(err)
	}
	terminalReadUntil(t, ws, "still here")
	ws.Close()
	terminalWaitDone(t, done)
	if _, err := os.Stat(device); err != nil {
		t.Fatal(err)
	}
	// and the device is free again, for the probe too
	if r := probeSerialConsole(device, 300*time.Millisecond); r.State == "busy" {
		t.Fatalf("still busy: %+v", r)
	}
}

func TestSerialSessionEndsWhenTheDeviceGoes(t *testing.T) {
	master, device := serialTestDevice(t)
	ws, done := serialTestSession(t, &serialSettings{Device: device, Baud: 9600, Parity: "none", Flow: "none", DataBits: 8, StopBits: 1})
	waitRaw(t, master)
	master.Close() // the cable is pulled: the slave reads EOF/EIO
	ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	var output strings.Builder
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			break
		}
		output.Write(data)
	}
	if !strings.Contains(output.String(), "disconnected") {
		t.Fatalf("no notice: %q", output.String())
	}
	terminalWaitDone(t, done)
}

func TestSerialSessionRefusesAMissingDevice(t *testing.T) {
	ws, done := serialTestSession(t, &serialSettings{Device: "/dev/ttyNANOKVM-none", Baud: 9600, Parity: "none", Flow: "none", DataBits: 8, StopBits: 1})
	ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, err := ws.ReadMessage()
	if err != nil || !strings.Contains(string(data), "not available") {
		t.Fatalf("%q %v", data, err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("no error for a missing device")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("session did not finish")
	}
	_ = io.EOF
}
