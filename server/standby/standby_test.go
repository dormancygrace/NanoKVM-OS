package standby

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"golang.org/x/sys/unix"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStateFailsClosed(t *testing.T) {
	s := NewState(Config{})
	if s.Get().Safe {
		t.Fatal("preparing is not safe")
	}
	for _, magic := range []bool{false, true} {
		if _, e := s.Restart(magic); e == nil {
			t.Fatal("accepted wake during transition")
		}
	}
	s.Fail(errors.New("unmount busy"))
	s.Ready()
	if s.Get().Safe || s.Get().State != "failed" {
		t.Fatal(s.Get())
	}
	if _, e := s.Restart(true); e == nil {
		t.Fatal("magic wake accepted failed transition")
	}
	first, e := s.Restart(false)
	if e != nil || !first {
		t.Fatal("authenticated failure recovery unavailable")
	}
}
func TestConcurrentWakeIsOnce(t *testing.T) {
	s := NewState(Config{})
	s.Ready()
	var count atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Go(func() {
			first, e := s.Restart(true)
			if e == nil && first {
				count.Add(1)
			}
		})
	}
	wg.Wait()
	if count.Load() != 1 || s.Get().Safe {
		t.Fatal(count.Load(), s.Get())
	}
}
func TestGateClosesAtomically(t *testing.T) {
	var g Gate
	if !g.Enter(true) {
		t.Fatal("first operation")
	}
	if g.Begin() == nil {
		t.Fatal("ignored active operation")
	}
	g.Leave(true)
	if e := g.Begin(); e != nil {
		t.Fatal(e)
	}
	if !g.Enter(false) || g.Enter(true) || g.EnterStream() || g.Begin() == nil {
		t.Fatal("admission was not closed")
	}
	g.Cancel()
	if !g.Enter(true) {
		t.Fatal("failed preflight did not reopen admission")
	}
	g.Leave(true)
}
func TestMagicPacket(t *testing.T) {
	mac := []byte{0x02, 0x11, 0x22, 0x33, 0x44, 0x55}
	valid := append(bytes.Repeat([]byte{255}, 6), bytes.Repeat(mac, 16)...)
	if !MagicPacket(valid, mac) || !MagicPacket(append([]byte{1, 2, 3}, valid...), mac) {
		t.Fatal("standard packet rejected")
	}
	for _, b := range [][]byte{valid[:len(valid)-1], bytes.Repeat(mac, 16), nil, []byte("POST /wake")} {
		if MagicPacket(b, mac) {
			t.Fatal("accepted invalid packet")
		}
	}
	for i := range valid {
		b := bytes.Clone(valid)
		b[i] ^= 1
		if MagicPacket(b, mac) {
			t.Fatalf("accepted corrupted byte %d", i)
		}
	}
	if MagicPacket(valid, []byte{2, 1, 2, 3, 4, 5}) || MagicPacket(valid, nil) {
		t.Fatal("wrong NIC")
	}
}
func TestHTTPAuthorizationAndPhases(t *testing.T) {
	c := Config{Token: strings.Repeat("ab", 32)}
	s := NewState(c)
	var calls atomic.Int32
	h := Handler(c, s, func() error { calls.Add(1); return nil })
	run := func(method, path, token, origin string, tlsOn bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://192.0.2.1"+path, nil)
		r.Header.Set("Authorization", token)
		r.Header.Set("Origin", origin)
		if tlsOn {
			r.TLS = &tls.ConnectionState{}
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	bearer := "Bearer " + c.Token
	for _, token := range []string{"", c.Token, "Bearer wrong"} {
		if w := run("POST", "/standby/start", token, "http://192.0.2.1", false); w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	for _, origin := range []string{"", "http://evil.example", "https://192.0.2.1", "http://192.0.2.1/path", "http://admin@192.0.2.1"} {
		if w := run("POST", "/standby/start", bearer, origin, false); w.Code != 403 {
			t.Fatal(origin, w.Code)
		}
	}
	if w := run("POST", "/standby/start", bearer, "http://192.0.2.1", false); w.Code != 409 {
		t.Fatal("transition wake", w.Code)
	}
	if w := run("GET", "/standby/start", bearer, "", false); w.Code != 404 {
		t.Fatal("GET can restart")
	}
	w := run("GET", "/standby/status", bearer, "", false)
	var status Status
	if e := json.Unmarshal(w.Body.Bytes(), &status); e != nil || status.Safe {
		t.Fatal(w.Body.String())
	}
	s.Ready()
	for i := 0; i < 2; i++ {
		if w := run("POST", "/standby/start", bearer, "https://192.0.2.1", true); w.Code != 202 {
			t.Fatal(w.Code)
		}
	}
	time.Sleep(350 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatal("duplicate reboot", calls.Load())
	}
	if w := run("GET", "/standby", bearer, "", false); strings.Contains(w.Body.String(), c.Token) {
		t.Fatal("key in HTML")
	}
}
func TestMountAndCounterParsing(t *testing.T) {
	m, e := ParseMounts("25 1 179:2 / / rw,relatime - f2fs /dev/mmcblk0p2 rw,background_gc=off\n26 25 0:11 / /run rw,nosuid - tmpfs tmpfs rw\n27 25 8:1 / /disk\\040name ro - ext4 /dev/sda1 rw\n")
	if e != nil || len(m) != 3 || m[2].Point != "/disk name" || m[2].SuperOptions != "rw" {
		t.Fatal(m, e)
	}
	if _, e = ParseMounts("truncated"); e == nil {
		t.Fatal("invalid mounts accepted")
	}
	c, e := ParseCounters("1 2 3 4 5 6 7 8 0 10 11 12 13 14 15 16 17")
	if e != nil || c.Writes != 5 || c.Written != 7 || c.Discards != 12 || c.Discarded != 14 || c.Flushes != 16 || c.InFlight != 0 {
		t.Fatal(c, e)
	}
	for _, b := range []string{"1 2", "1 2 3 4 bad 6 7 8 0 10 11 12 13 14 15 16 17"} {
		if _, e := ParseCounters(b); e == nil {
			t.Fatal("malformed counters accepted")
		}
	}
}
func TestHandoffPreservesInitAndRejectsCustomRestart(t *testing.T) {
	base := "::sysinit:/sbin/openrc sysinit\n::shutdown:/sbin/openrc shutdown\nttyS0::respawn:/sbin/getty ttyS0\n"
	table, e := handoffInittab(base)
	if e != nil || !strings.HasPrefix(table, strings.ReplaceAll(base, "::shutdown:/sbin/openrc shutdown", "::shutdown:"+Root+"/init shutdown")) || !strings.Contains(table, "::restart:"+Root+"/init takeover") || !strings.Contains(table, "::respawn:"+Root+"/init acknowledge") {
		t.Fatal(table, e)
	}
	if _, e := handoffInittab(base + "::restart:/custom\n"); e == nil {
		t.Fatal("overrode custom restart")
	}
	if _, e := handoffInittab("::shutdown:/custom"); e == nil {
		t.Fatal("accepted unrecognized shutdown")
	}
}
func TestWPAHasNoStorageDependencies(t *testing.T) {
	valid := []byte("ctrl_interface=/var/run/wpa_supplicant\nnetwork={\nssid=666f6f\npsk=\"test\"\nkey_mgmt=WPA-PSK\nieee80211w=1\n}\n")
	b, e := ramWPA(valid)
	if e != nil || !bytes.Contains(b, []byte("update_config=0")) || bytes.Contains(b, []byte("/var/")) {
		t.Fatal(string(b), e)
	}
	for _, line := range []string{"include=/etc/wifi", "ca_cert=/etc/cert", "engine=1", "private_key=/data/key"} {
		if _, e = ramWPA(append(bytes.Clone(valid), []byte(line)...)); e == nil {
			t.Fatal("accepted external dependency", line)
		}
	}
}
func TestDefaultGateway(t *testing.T) {
	g, e := defaultGateway("Iface Destination Gateway Flags RefCnt Use Metric Mask\neth0 00000000 010200C0 0003 0 0 100 00000000\n", "eth0")
	if e != nil || g != "192.0.2.1" {
		t.Fatal(g, e)
	}
}
func TestTakeoverCannotRunAsOrdinaryProcess(t *testing.T) {
	if os.Getpid() == 1 {
		t.Skip("not an ordinary test process")
	}
	if e := Takeover(); e == nil {
		t.Fatal("accepted non-PID1 takeover")
	}
}
func TestQueuedMagicNeverWakesAfterReady(t *testing.T) {
	s := NewState(Config{})
	before := time.Now()
	s.Ready()
	if _, e := s.MagicRestart(before); e == nil {
		t.Fatal("queued transition packet accepted")
	}
	if _, e := s.MagicRestart(time.Time{}); e == nil {
		t.Fatal("missing timestamp accepted")
	}
	if first, e := s.MagicRestart(time.Now()); e != nil || !first {
		t.Fatal("fresh packet rejected", e)
	}
}

func TestOpenRCFailureSurvivesCleanup(t *testing.T) {
	directory := t.TempDir()
	w, e := watchFailures(directory)
	if e != nil {
		t.Fatal(e)
	}
	defer unix.Close(w.fd)
	if e = w.result(); e != nil {
		t.Fatal(e)
	}
	path := directory + "/writer"
	if e = os.Symlink("/etc/init.d/writer", path); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if e = w.result(); e == nil || !strings.Contains(e.Error(), "writer") {
		t.Fatal("lost transient stop failure", e)
	}
}

func TestRejectsRoutesThatWouldDisappear(t *testing.T) {
	c := Config{Address: "192.0.2.5", Prefix: 24, Interface: "eth0"}
	base := "eth0 00000000 010200C0 0003 0 0 100 00000000\neth0 000200C0 00000000 0001 0 0 100 00FFFFFF\n"
	if e := simpleRoutes(base, c); e != nil {
		t.Fatal(e)
	}
	if e := simpleRoutes(base+"eth0 0000010A 010200C0 0003 0 0 100 00FFFFFF\n", c); e == nil {
		t.Fatal("accepted classless route")
	}
}
func TestProcessReleaseRequiresZombieOrKernelThread(t *testing.T) {
	for _, tc := range []struct {
		stat string
		want bool
	}{
		{"4 (worker ) name) D 1 1 1 0 0 0", false},
		{"4 (worker) S 1 1 1 0 0 0", false},
		{"4 (worker) Z 1 1 1 0 0 0", true},
		{"4 (kworker) I 1 1 1 0 0 2097152", true},
	} {
		got, e := processReleased(tc.stat)
		if e != nil || got != tc.want {
			t.Fatal(tc, got, e)
		}
	}
	if _, e := processReleased("broken"); e == nil {
		t.Fatal("accepted invalid process stat")
	}
}

func TestLateFailureDoesNotDuplicateRestart(t *testing.T) {
	s := NewState(Config{})
	s.Ready()
	if first, e := s.Restart(false); !first || e != nil {
		t.Fatal(first, e)
	}
	s.Fail(errors.New("network exited during reboot"))
	if s.Get().State != "restarting" {
		t.Fatal(s.Get())
	}
	if first, e := s.Restart(false); first || e != nil {
		t.Fatal("duplicate reboot", first, e)
	}
	s.RestartFailed(errors.New("reboot syscall failed"))
	if s.Get().State != "failed" || s.Get().Safe {
		t.Fatal(s.Get())
	}
	if first, e := s.Restart(false); !first || e != nil {
		t.Fatal("recovery retry unavailable", first, e)
	}
}

func TestWatchdogOptionalSysfsAndUserspaceOwnership(t *testing.T) {
	for _, tc := range []struct {
		name, state   string
		missing, fail bool
	}{
		{"sysfs-disabled", "", true, false},
		{"clean-magic-close", "inactive\n", false, false},
		{"userspace-deadline-still-active", "active\n", false, true},
		{"malformed", "", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			device := filepath.Join(root, "watchdog0")
			if e := os.Mkdir(device, 0700); e != nil {
				t.Fatal(e)
			}
			if !tc.missing {
				if e := os.WriteFile(filepath.Join(device, "state"), []byte(tc.state), 0600); e != nil {
					t.Fatal(e)
				}
			}
			if e := watchdogsReleased(root); (e != nil) != tc.fail {
				t.Fatal(e)
			}
		})
	}
	root := t.TempDir()
	if e := os.MkdirAll(filepath.Join(root, "watchdog0", "state"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := watchdogsReleased(root); e == nil {
		t.Fatal("ignored a non-ENOENT read failure")
	}
	if e := watchdogsReleased(filepath.Join(root, "absent")); e != nil {
		t.Fatal(e)
	}
}

func TestFatalRecoveryRequiresCompletedStorageProtection(t *testing.T) {
	ordinary := errors.New("storage release failed")
	waited, reboots := 0, 0
	wait := func(d time.Duration) {
		if d != 2*time.Minute {
			t.Fatal(d)
		}
		waited++
	}
	reboot := func() error { reboots++; return nil }
	if got := recoverFatal(ordinary, wait, reboot); got != ordinary || waited != 0 || reboots != 0 {
		t.Fatal(got, waited, reboots)
	}
	if got := recoverFatal(nil, wait, reboot); got != nil || waited != 0 || reboots != 0 {
		t.Fatal(got, waited, reboots)
	}
	safe := &RecoveryError{Cause: errors.New("listener failed")}
	if got := recoverFatal(safe, wait, reboot); got != nil || waited != 1 || reboots != 1 {
		t.Fatal(got, waited, reboots)
	}
	if got := recoverFatal(safe, wait, func() error { return errors.New("reboot refused") }); got == nil || !strings.Contains(got.Error(), "reboot refused") {
		t.Fatal(got)
	}
}
