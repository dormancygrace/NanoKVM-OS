package vm

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
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
	// Raw termios alone does not mean startup is finished: openSerial flushes
	// the tty afterwards. Send test traffic only once that flush has completed.
	ready := make(chan struct{})
	var once sync.Once
	markReady := func() { once.Do(func() { close(ready) }) }
	ws, done := serialTestSessionWithRunner(t, s, func(ws *websocket.Conn, s *serialSettings) error {
		defer markReady() // missing/busy devices finish without entering poll
		return runSerialSessionWithPoll(ws, s, func(fds []unix.PollFd, timeout int) (int, error) {
			markReady()
			return unix.Poll(fds, timeout)
		})
	})
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("the serial session did not finish opening the device")
	}
	return ws, done
}

func serialTestSessionWithRunner(t *testing.T, s *serialSettings, run func(*websocket.Conn, *serialSettings) error) (*websocket.Conn, <-chan error) {
	t.Helper()
	done := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			done <- err
			return
		}
		defer ws.Close()
		done <- run(ws, s)
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
	if got := terminalReadUntil(t, second, "in use"); !strings.Contains(got, "another session") {
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

// A big browser write that the device does not take, while the device
// side sends and then goes away: both ends write notices to the WebSocket
// at once. One writer, so no "concurrent write to websocket connection".
func TestSerialSessionDisconnectDuringTraffic(t *testing.T) {
	master, device := serialTestDevice(t)
	ws, done := serialTestSession(t, &serialSettings{Device: device, Baud: 9600, Parity: "none", Flow: "none", DataBits: 8, StopBits: 1})
	waitRaw(t, master)
	// the device side talks the whole time
	talking := make(chan struct{})
	go func() {
		defer close(talking)
		for i := 0; i < 200; i++ {
			if _, err := master.Write([]byte("chatter from the device side\r\n")); err != nil {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	// the browser pastes more than the pty takes while the master does not read
	if err := ws.WriteMessage(websocket.TextMessage, bytes.Repeat([]byte("x"), maxReadSize)); err != nil {
		t.Fatal(err)
	}
	// the browser keeps reading what comes back
	go func() {
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}()
	time.Sleep(100 * time.Millisecond)
	master.Close() // the device goes while the write is still blocked
	<-talking
	terminalWaitDone(t, done)
	if serialPortOpen(device) {
		t.Fatal("the lease outlived the session")
	}
}

// The browser closes while the device does not take data: the WebSocket
// reader is not the one waiting on the tty, so the close is seen, the
// write is cancelled and the device is released.
func TestSerialSessionReleasesTheDeviceWhenTheBrowserGoesDuringABlockedWrite(t *testing.T) {
	saved := serialPingInterval
	serialPingInterval = 50 * time.Millisecond
	t.Cleanup(func() { serialPingInterval = saved })
	master, device := serialTestDevice(t)
	ws, done := serialTestSession(t, &serialSettings{Device: device, Baud: 9600, Parity: "none", Flow: "none", DataBits: 8, StopBits: 1})
	waitRaw(t, master)
	// the master never reads: the tty's output queue fills and the write blocks
	if err := ws.WriteMessage(websocket.TextMessage, bytes.Repeat([]byte("y"), maxReadSize)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if !serialPortOpen(device) {
		t.Fatal("the session ended on its own")
	}
	ws.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the session did not end after the browser closed")
	}
	if serialPortOpen(device) {
		t.Fatal("the device is still claimed after the browser closed")
	}
}

// An interrupted wait is not a readable tty. With VMIN=0 a premature read
// can return zero bytes, which would falsely report that the device vanished.
func TestSerialSessionRetriesInterruptedDevicePoll(t *testing.T) {
	master, device := serialTestDevice(t)
	retried := make(chan struct{})
	calls := 0
	poll := func(fds []unix.PollFd, timeout int) (int, error) {
		calls++
		if calls == 1 {
			return -1, unix.EINTR
		}
		if calls == 2 {
			close(retried)
		}
		return unix.Poll(fds, timeout)
	}
	ws, done := serialTestSessionWithRunner(t,
		&serialSettings{Device: device, Baud: 9600, Parity: "none", Flow: "none", DataBits: 8, StopBits: 1},
		func(ws *websocket.Conn, s *serialSettings) error { return runSerialSessionWithPoll(ws, s, poll) })
	defer ws.Close()
	select {
	case <-retried:
	case err := <-done:
		t.Fatalf("the session ended after EINTR instead of retrying: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("the device poll was not retried after EINTR")
	}
	if !serialPortOpen(device) {
		t.Fatal("the interrupted wait released the device")
	}
	if _, err := master.Write([]byte("device after EINTR")); err != nil {
		t.Fatal(err)
	}
	if got := terminalReadUntil(t, ws, "device after EINTR"); got != "device after EINTR" {
		t.Fatalf("device output after EINTR: %q", got)
	}
	if err := ws.WriteMessage(websocket.TextMessage, []byte("browser after EINTR")); err != nil {
		t.Fatal(err)
	}
	if got := string(readMaster(t, master, len("browser after EINTR"))); got != "browser after EINTR" {
		t.Fatalf("browser input after EINTR: %q", got)
	}
	ws.Close()
	terminalWaitDone(t, done)
	if serialPortOpen(device) {
		t.Fatal("the device is still claimed after the browser closed")
	}
}

func serialPortOpen(device string) bool {
	serialPorts.Lock()
	defer serialPorts.Unlock()
	_, open := serialPorts.open[device]
	return open
}
