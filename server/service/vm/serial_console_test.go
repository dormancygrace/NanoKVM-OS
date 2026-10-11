package vm

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/gin-gonic/gin"

	"NanoKVM-Server/proto"
)

func TestClassifySerialResponse(t *testing.T) {
	for _, tc := range []struct {
		in          string
		state, text string
	}{
		{"", "silent", ""},
		{"\r\n\r\nDebian GNU/Linux 12 host ttyACM0\r\n\r\nhost login: ", "login", "Debian GNU/Linux 12 host ttyACM0 host login:"},
		{"\x1b[?2004h\x1b[32muser@host\x1b[0m:~$ ", "shell", "user@host:~$"},
		{"\x1b]0;title\x07root@host:~# ", "shell", "root@host:~#"},
		{"Password: ", "password", "Password:"},
		{"\r\n", "silent", ""},
		{"some banner without a prompt", "text", "some banner without a prompt"},
		// a getty's terminal setup before the prompt (Arch, Ubuntu 26.04): nothing readable
		{terminalSetup, "silent", ""},
		{terminalSetup + "nkvm-test-arch login: ", "login", "nkvm-test-arch login:"},
	} {
		state, text := classifySerialResponse([]byte(tc.in))
		if state != tc.state || text != tc.text {
			t.Errorf("%q: got %s %q, want %s %q", tc.in, state, text, tc.state, tc.text)
		}
	}
	state, text := classifySerialResponse([]byte(strings.Repeat("x", 300) + " login: "))
	if state != "login" || len(text) != 200 || !strings.HasSuffix(text, "login:") {
		t.Fatalf("long response: %s %d", state, len(text))
	}
}

// terminalSetup is what the getty on Arch and Ubuntu 26.04 sent before the
// prompt (captured on the NanoKVM side): a soft reset, colours reset, a jump
// to the far corner and a cursor position query, twice, then an OSC with the
// session context. The prompt follows once the terminal has answered.
const terminalSetup = "\r\n\x1b[!p\x1b]104\x1b\\\x1b[0m\x1b[?7h\x1b[1G\x1b[0J\x1b7\x1b[32766;32766H\x1b[6n\x1b8" +
	"\x1b[!p\x1b]104\x1b\\\x1b[0m\x1b[?7h\x1b[1G\x1b[0J\x1b7\x1b[32766;32766H\x1b[6n\x1b8" +
	"\x1b]3008;start=bdb55e01;user=root;hostname=nkvm-test-arch;machineid=517;bootid=97f\x1b\\"

// fakeHost answers the first carriage return on the master side of a pty
// the way a getty would, or stays silent.
func fakeHost(t *testing.T, answer string) (string, func() []byte) {
	return fakeHostWithSetup(t, "", answer)
}

// fakeHostWithSetup first sends setup after the carriage return and, when
// that asked for the cursor position, waits for the reply before the answer.
func fakeHostWithSetup(t *testing.T, setup, answer string) (string, func() []byte) {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { master.Close(); slave.Close() })
	received := make(chan []byte, 1)
	readUntil := func(got []byte, stop string) []byte {
		buf := make([]byte, 64)
		for !strings.Contains(string(got), stop) {
			n, err := master.Read(buf)
			if n > 0 {
				got = append(got, buf[:n]...)
			}
			if err != nil {
				break
			}
		}
		return got
	}
	go func() {
		got := readUntil(nil, "\r")
		if setup != "" {
			master.Write([]byte(setup))
			for i := 0; i < strings.Count(setup, string(cursorPositionQuery)); i++ {
				got = readUntil(got, "R")
				got = []byte(strings.Replace(string(got), string(cursorPositionReply), "<cpr>", 1))
			}
		}
		if answer != "" {
			master.Write([]byte(answer))
		}
		received <- got
	}()
	return slave.Name(), func() []byte {
		select {
		case got := <-received:
			return got
		case <-time.After(3 * time.Second):
			t.Fatal("the host never received the probe")
			return nil
		}
	}
}

func TestProbeSerialConsoleLoginPrompt(t *testing.T) {
	serialLockDir = t.TempDir()
	device, received := fakeHost(t, "\r\nDebian GNU/Linux 12 host ttyACM0\r\n\r\nhost login: ")
	result := probeSerialConsole(device, 3*time.Second)
	if result.State != "login" || result.Err != nil {
		t.Fatalf("%+v", result)
	}
	if !strings.HasSuffix(result.Response, "host login:") {
		t.Fatalf("response %q", result.Response)
	}
	if got := received(); string(got) != "\r" {
		t.Fatalf("the host received %q", got)
	}
}

func TestProbeSerialConsoleAnswersCursorPositionQuery(t *testing.T) {
	serialLockDir = t.TempDir()
	device, received := fakeHostWithSetup(t, terminalSetup, "\r\nArch Linux 7.2.9-arch1-1 (ttyACM0)\r\n\r\nnkvm-test-arch login: ")
	result := probeSerialConsole(device, 3*time.Second)
	if result.State != "login" || result.Err != nil {
		t.Fatalf("%+v", result)
	}
	// the setup leaves no text behind (ESC 7 and ESC 8 are not "7 8")
	if result.Response != "Arch Linux 7.2.9-arch1-1 (ttyACM0) nkvm-test-arch login:" {
		t.Fatalf("response %q", result.Response)
	}
	// one reply per query, nothing else
	if got := received(); string(got) != "\r<cpr><cpr>" {
		t.Fatalf("the host received %q", got)
	}
}

func TestProbeSerialConsoleHoldsTheDeviceForItsWholeRun(t *testing.T) {
	serialLockDir = t.TempDir()
	device, received := fakeHost(t, "")
	first := make(chan probeResult, 1)
	go func() { first <- probeSerialConsole(device, 600*time.Millisecond) }()
	received() // the first probe has the device open and is waiting for an answer
	if r := probeSerialConsole(device, 200*time.Millisecond); r.State != "busy" {
		t.Fatalf("overlapping probe: %+v", r)
	}
	if serialPortOpen(device) != true {
		t.Fatal("the running probe holds no lease")
	}
	select {
	case r := <-first:
		if r.State != "silent" {
			t.Fatalf("first probe: %+v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the first probe did not finish")
	}
	if serialPortOpen(device) {
		t.Fatal("the lease outlived the probe")
	}
	if r := probeSerialConsole(device, 200*time.Millisecond); r.State != "silent" {
		t.Fatalf("after the first probe: %+v", r)
	}
}

func TestProbeSerialConsoleShellPrompt(t *testing.T) {
	serialLockDir = t.TempDir()
	device, _ := fakeHost(t, "\r\n\x1b[01;32muser@host\x1b[00m:~$ ")
	result := probeSerialConsole(device, 3*time.Second)
	if result.State != "shell" || result.Response != "user@host:~$" {
		t.Fatalf("%+v", result)
	}
}

func TestProbeSerialConsoleSilentHost(t *testing.T) {
	serialLockDir = t.TempDir()
	device, received := fakeHost(t, "")
	start := time.Now()
	result := probeSerialConsole(device, 500*time.Millisecond)
	if result.State != "silent" || result.Response != "" {
		t.Fatalf("%+v", result)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("did not stop at the timeout")
	}
	received()
}

func TestProbeSerialConsoleUnavailableAndBusy(t *testing.T) {
	serialLockDir = t.TempDir()
	if r := probeSerialConsole(filepath.Join(t.TempDir(), "ttyGS0"), time.Second); r.State != "unavailable" {
		t.Fatalf("missing device: %+v", r)
	}
	device, _ := fakeHost(t, "login: ")
	if err := os.WriteFile(serialLockFile(device), []byte("123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := probeSerialConsole(device, time.Second); r.State != "busy" {
		t.Fatalf("locked port: %+v", r)
	}
	os.Remove(serialLockFile(device))

	// another process holding the port open counts as busy without a lock
	holder, err := os.OpenFile(device, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	cmd := exec.Command("sleep", "30")
	cmd.Stdin = holder
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	if r := probeSerialConsole(device, time.Second); r.State != "busy" {
		t.Fatalf("port held by another process: %+v", r)
	}
}

func setupSerialConsoleFiles(t *testing.T, script, disk bool) {
	t.Helper()
	share := t.TempDir()
	serialConsoleShareDir = share
	serial := filepath.Join(t.TempDir(), "serialnumber")
	gadgetSerialNumberPath = serial
	serialConsoleLunFile = filepath.Join(t.TempDir(), "file")
	os.WriteFile(serial, []byte("d19506b31359c372\n"), 0o644)
	if script {
		os.WriteFile(filepath.Join(share, serialConsoleScriptName), []byte("#!/bin/sh\necho helper\n"), 0o755)
	}
	if disk {
		os.WriteFile(filepath.Join(share, serialConsoleDiskName), []byte("FAT12 image bytes"), 0o644)
	}
}

func TestSerialConsoleState(t *testing.T) {
	setupSerialConsoleFiles(t, true, true)
	state := serialConsoleState()
	if state.SerialNumber != "d19506b31359c372" || state.Port != "/dev/ttyGS0" || state.Script.Path != "/nanokvm-serial-console.sh" {
		t.Fatalf("%+v", state)
	}
	if !state.Script.Available || state.Script.Size != 22 || len(state.Script.Sha256) != 64 {
		t.Fatalf("script: %+v", state.Script)
	}
	if !state.Disk.Available || state.Disk.Attached || state.Disk.File != filepath.Join(serialConsoleShareDir, serialConsoleDiskName) {
		t.Fatalf("disk: %+v", state.Disk)
	}
	// attached once the gadget's helper LUN is backed by the shipped image
	os.WriteFile(serialConsoleLunFile, []byte("/elsewhere/other.img\n"), 0o644)
	if serialConsoleState().Disk.Attached {
		t.Fatal("another image counted as the helper disk")
	}
	os.WriteFile(serialConsoleLunFile, []byte(state.Disk.File+"\n"), 0o644)
	if s := serialConsoleState().Disk; !s.Attached || s.Stale {
		t.Fatalf("not attached: %+v", s)
	}
	// the image was replaced under the LUN (a package upgrade)
	os.WriteFile(serialConsoleLunFile, []byte(state.Disk.File+" (deleted)\n"), 0o644)
	if s := serialConsoleState().Disk; !s.Attached || !s.Stale {
		t.Fatalf("replaced image: %+v", s)
	}
}

func TestSerialConsoleWithoutShippedFiles(t *testing.T) {
	setupSerialConsoleFiles(t, false, false)
	state := serialConsoleState()
	if state.Script.Available || state.Disk.Available || state.Disk.Attached {
		t.Fatalf("%+v", state)
	}
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("GET", SerialConsoleScriptPath, nil)
	(&Service{}).ServeSerialConsoleScript(c)
	if recorder.Code != 404 {
		t.Fatalf("code %d", recorder.Code)
	}
}

func TestServeSerialConsoleScript(t *testing.T) {
	setupSerialConsoleFiles(t, true, false)
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("GET", SerialConsoleScriptPath, nil)
	(&Service{}).ServeSerialConsoleScript(c)
	body, _ := io.ReadAll(recorder.Body)
	if recorder.Code != 200 || string(body) != "#!/bin/sh\necho helper\n" {
		t.Fatalf("%d %q", recorder.Code, body)
	}
	h := recorder.Header()
	if !strings.HasPrefix(h.Get("Content-Type"), "text/x-shellscript") || h.Get("Cache-Control") != "no-cache" ||
		h.Get("X-Checksum-Sha256") != serialConsoleState().Script.Sha256 || !strings.Contains(h.Get("Content-Disposition"), serialConsoleScriptName) {
		t.Fatalf("headers %v", h)
	}
}

func TestTestSerialConsoleHandler(t *testing.T) {
	old := serialConsoleProbe
	t.Cleanup(func() { serialConsoleProbe = old })
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		result probeResult
		ok     bool
	}{
		{probeResult{State: "login", Response: "host login:"}, true},
		{probeResult{State: "shell", Response: "root@host:~#"}, true},
		{probeResult{State: "silent"}, false},
		{probeResult{State: "busy"}, false},
		{probeResult{State: "unavailable", Err: os.ErrNotExist}, false},
	} {
		serialConsoleProbe = func(string, time.Duration) probeResult { return tc.result }
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest("POST", "/api/vm/serial-console/test", nil)
		(&Service{}).TestSerialConsole(c)
		var rsp struct {
			Code int                        `json:"code"`
			Data proto.SerialConsoleTestRsp `json:"data"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &rsp); err != nil || rsp.Code != 0 {
			t.Fatalf("%s", recorder.Body.String())
		}
		if rsp.Data.Ok != tc.ok || rsp.Data.State != tc.result.State || rsp.Data.Response != tc.result.Response {
			t.Fatalf("%+v: %+v", tc.result, rsp.Data)
		}
	}
}
