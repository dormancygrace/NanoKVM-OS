package vm

import (
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
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
