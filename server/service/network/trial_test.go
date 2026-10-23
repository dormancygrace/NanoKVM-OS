package network

import (
	"encoding/json"
	"errors"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"NanoKVM-Server/proto"

	"github.com/gin-gonic/gin"
)

func TestMain(m *testing.M) {
	// The real record lives on the boot partition.
	dir, err := os.MkdirTemp("", "nanokvm-network-trial")
	if err != nil {
		panic(err)
	}
	trialFile = filepath.Join(dir, "net-trial.json")
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// endTrialForTest drops a trial a test left running, without restoring.
func endTrialForTest() {
	trialMu.Lock()
	defer trialMu.Unlock()
	if active != nil && active.timer != nil {
		active.timer.Stop()
	}
	active = nil
	_ = os.Remove(trialFile)
}

type trialFixture struct {
	dir       string
	restarted atomic.Int32
}

func newTrialFixture(t *testing.T) *trialFixture {
	t.Helper()
	f := &trialFixture{dir: t.TempDir()}
	oldConfig, oldDisabled, oldVLAN := ethernetConfigFile, ethernetDisabledFile, ethernetVLANFile
	oldRestart, oldDuration, oldAddrs, oldMDNS := restartEthernet, trialDuration, interfaceAddrs, mdnsHost
	ethernetConfigFile = filepath.Join(f.dir, "eth.nodhcp")
	ethernetDisabledFile = filepath.Join(f.dir, "eth.disabled")
	ethernetVLANFile = filepath.Join(f.dir, "eth.vlan")
	restartEthernet = func() error { f.restarted.Add(1); return nil }
	oldDelay := restartDelay
	restartDelay = 0
	t.Cleanup(func() { restartDelay = oldDelay })
	trialDuration = 200 * time.Millisecond
	interfaceAddrs = func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.IPv4(127, 0, 0, 1), Mask: net.CIDRMask(8, 32)},
			&net.IPNet{IP: net.IPv4(192, 168, 1, 50), Mask: net.CIDRMask(24, 32)},
			&net.IPNet{IP: net.IPv4(10, 0, 0, 9), Mask: net.CIDRMask(24, 32)},
			&net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)},
		}, nil
	}
	mdnsHost = func() string { return "nanokvm.local" }
	t.Cleanup(func() {
		endTrialForTest()
		ethernetConfigFile, ethernetDisabledFile, ethernetVLANFile = oldConfig, oldDisabled, oldVLAN
		restartEthernet, trialDuration, interfaceAddrs, mdnsHost = oldRestart, oldDuration, oldAddrs, oldMDNS
	})
	// The device now: static 192.168.1.50/24 on VLAN 12.
	if err := os.WriteFile(ethernetConfigFile, []byte("192.168.1.50/24 192.168.1.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ethernetVLANFile, []byte("12\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", f.dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := os.WriteFile(filepath.Join(f.dir, "sync"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *trialFixture) read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "<absent>"
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func postJSON(handler gin.HandlerFunc, body string) (*httptest.ResponseRecorder, proto.Response) {
	recorder := httptest.NewRecorder()
	router := gin.New()
	router.POST("/", handler)
	request := httptest.NewRequest("POST", "/", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Host = "192.168.1.50"
	router.ServeHTTP(recorder, request)
	var response proto.Response
	_ = json.Unmarshal(recorder.Body.Bytes(), &response)
	return recorder, response
}

type trialResponse struct {
	Trial trialInfo `json:"trial"`
}

func setEthernetOnTrial(t *testing.T, body string) trialInfo {
	t.Helper()
	recorder, response := postJSON((&Service{}).SetEthernet, body)
	if response.Code != 0 {
		t.Fatalf("set ethernet: %s", recorder.Body.String())
	}
	data, _ := json.Marshal(response.Data)
	var got trialResponse
	if err := json.Unmarshal(data, &got); err != nil || got.Trial.Token == "" || got.Trial.ID == "" {
		t.Fatalf("no trial in %s", recorder.Body.String())
	}
	return got.Trial
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitApplied(t *testing.T) {
	t.Helper()
	waitFor(t, "the change to be applied", func() bool {
		trialMu.Lock()
		defer trialMu.Unlock()
		return active != nil && active.isApplied
	})
}

const newStatic = `{"enabled":true,"mode":"static","address":"10.20.0.5","subnetMask":"255.255.0.0","gateway":"10.20.0.1","vlanEnabled":false,"vlanId":0}`

func TestAnUnconfirmedEthernetChangeIsUndone(t *testing.T) {
	f := newTrialFixture(t)
	info := setEthernetOnTrial(t, newStatic)
	if want := []string{"10.20.0.5", "192.168.1.50", "nanokvm.local", "10.0.0.9"}; strings.Join(info.Hosts, " ") != strings.Join(want, " ") {
		t.Fatalf("hosts %v, want %v", info.Hosts, want)
	}
	if got := f.read(t, ethernetConfigFile); got != "10.20.0.5/16 10.20.0.1\n" {
		t.Fatalf("new config not applied: %q", got)
	}
	if f.read(t, ethernetVLANFile) != "<absent>" || f.read(t, trialFile) == "<absent>" {
		t.Fatal("VLAN not removed or no trial record")
	}
	waitFor(t, "the change to be undone", func() bool { return f.read(t, trialFile) == "<absent>" })
	if got := f.read(t, ethernetConfigFile); got != "192.168.1.50/24 192.168.1.1\n" {
		t.Fatalf("config not restored: %q", got)
	}
	if got := f.read(t, ethernetVLANFile); got != "12\n" {
		t.Fatalf("VLAN not restored: %q", got)
	}
	if f.read(t, ethernetDisabledFile) != "<absent>" {
		t.Fatal("a file that did not exist was left behind")
	}
	// Once for the change (the handler's own restart runs in a goroutine), once for the revert.
	waitFor(t, "two restarts", func() bool { return f.restarted.Load() == 2 })
}

func TestAConfirmedChangeStaysAndOnlyItsTokenConfirms(t *testing.T) {
	f := newTrialFixture(t)
	info := setEthernetOnTrial(t, newStatic)
	waitApplied(t)
	if _, response := postJSON((&Service{}).ConfirmTrial, `{"token":"`+strings.Repeat("0", 32)+`"}`); response.Code == 0 {
		t.Fatal("a wrong token confirmed the change")
	}
	// A second change waits for the first.
	if _, response := postJSON((&Service{}).SetEthernet, `{"enabled":false,"mode":"dhcp"}`); response.Code != -3 {
		t.Fatalf("second change accepted during a trial: %+v", response)
	}
	if f.read(t, ethernetDisabledFile) != "<absent>" {
		t.Fatal("the refused change was written")
	}
	if _, response := postJSON((&Service{}).ConfirmTrial, `{"token":"`+info.Token+`"}`); response.Code != 0 {
		t.Fatalf("confirm failed: %+v", response)
	}
	if _, response := postJSON((&Service{}).ConfirmTrial, `{"token":"`+info.Token+`"}`); response.Code == 0 {
		t.Fatal("a token confirmed twice")
	}
	time.Sleep(2 * trialDuration)
	if got := f.read(t, ethernetConfigFile); got != "10.20.0.5/16 10.20.0.1\n" {
		t.Fatalf("a confirmed change was undone: %q", got)
	}
	if f.read(t, trialFile) != "<absent>" {
		t.Fatal("the trial record stayed")
	}
	waitFor(t, "the change's restart", func() bool { return f.restarted.Load() == 1 })
	time.Sleep(50 * time.Millisecond)
	if f.restarted.Load() != 1 {
		t.Fatal("a confirmed change was restarted again")
	}
}

func TestReachingTheDeviceBeforeTheRestartConfirmsNothing(t *testing.T) {
	f := newTrialFixture(t)
	release := make(chan struct{})
	restartEthernet = func() error {
		<-release
		f.restarted.Add(1)
		return nil
	}
	info := setEthernetOnTrial(t, newStatic)
	// The device still runs its previous settings: the page can reach it
	// at its old address, which must not count.
	if _, response := postJSON((&Service{}).ConfirmTrial, `{"token":"`+info.Token+`"}`); response.Code == 0 {
		t.Fatal("confirmed before the change was applied")
	}
	close(release)
	waitApplied(t)
	if _, response := postJSON((&Service{}).ConfirmTrial, `{"token":"`+info.Token+`"}`); response.Code != 0 {
		t.Fatalf("confirm after the restart failed: %+v", response)
	}
}

func TestTheProbeShowsOnlyTheTrialID(t *testing.T) {
	newTrialFixture(t)
	probe := func() (string, string) {
		recorder := httptest.NewRecorder()
		router := gin.New()
		router.GET("/", (&Service{}).ProbeTrial)
		router.ServeHTTP(recorder, httptest.NewRequest("GET", "/", nil))
		return recorder.Header().Get("Access-Control-Allow-Origin"), recorder.Body.String()
	}
	if origin, body := probe(); origin != "*" || !strings.Contains(body, `"id":""`) {
		t.Fatalf("probe without a trial: %s %s", origin, body)
	}
	info := setEthernetOnTrial(t, newStatic)
	waitApplied(t)
	_, body := probe()
	if !strings.Contains(body, `"id":"`+info.ID+`"`) || !strings.Contains(body, `"applied":true`) || strings.Contains(body, info.Token) {
		t.Fatalf("probe during a trial: %s", body)
	}
}

func TestATrialLeftByARestartIsUndoneAtStart(t *testing.T) {
	f := newTrialFixture(t)
	previous := "192.168.7.7/24 none\n"
	record, _ := json.Marshal(trialRecord{Service: "ethernet", Files: map[string]*string{
		ethernetConfigFile: &previous, ethernetVLANFile: nil, ethernetDisabledFile: nil,
	}})
	if err := os.WriteFile(trialFile, record, 0o600); err != nil {
		t.Fatal(err)
	}
	RestorePendingTrial()
	waitFor(t, "the restart", func() bool { return f.restarted.Load() == 1 })
	if got := f.read(t, ethernetConfigFile); got != previous {
		t.Fatalf("config %q", got)
	}
	if f.read(t, ethernetVLANFile) != "<absent>" || f.read(t, trialFile) != "<absent>" {
		t.Fatal("VLAN or record left")
	}

	// A record naming any other file is dropped, not followed.
	other := filepath.Join(f.dir, "passwd")
	contents := "x"
	record, _ = json.Marshal(trialRecord{Service: "ethernet", Files: map[string]*string{other: &contents}})
	if err := os.WriteFile(trialFile, record, 0o600); err != nil {
		t.Fatal(err)
	}
	RestorePendingTrial()
	if f.read(t, other) != "<absent>" || f.read(t, trialFile) != "<absent>" {
		t.Fatal("a foreign path was restored or the record kept")
	}
}

func TestAFailedRestartUndoesTheChangeAtOnce(t *testing.T) {
	f := newTrialFixture(t)
	// Long enough that only the failed restart can undo it in time.
	trialDuration = 10 * time.Second
	restartEthernet = func() error {
		f.restarted.Add(1)
		if f.restarted.Load() == 1 {
			return errors.New("S30eth failed")
		}
		return nil
	}
	info := setEthernetOnTrial(t, newStatic)
	waitFor(t, "the change to be undone", func() bool { return f.read(t, trialFile) == "<absent>" })
	if got := f.read(t, ethernetConfigFile); got != "192.168.1.50/24 192.168.1.1\n" {
		t.Fatalf("config not restored: %q", got)
	}
	if _, response := postJSON((&Service{}).ConfirmTrial, `{"token":"`+info.Token+`"}`); response.Code == 0 {
		t.Fatal("a change whose restart failed was confirmed")
	}
	waitFor(t, "the restart with the previous settings", func() bool { return f.restarted.Load() == 2 })
}

func TestNoChangeStartsWhileAnotherIsBeingUndone(t *testing.T) {
	f := newTrialFixture(t)
	release := make(chan struct{})
	reverting := make(chan struct{}, 1)
	// The change's restart is quick; the revert's is slow, and meanwhile the
	// record and the trial stay.
	restartEthernet = func() error {
		if f.restarted.Add(1) == 1 {
			return nil
		}
		reverting <- struct{}{}
		<-release
		return nil
	}
	setEthernetOnTrial(t, newStatic)
	waitApplied(t)
	select {
	case <-reverting:
	case <-time.After(3 * time.Second):
		t.Fatal("the change was not undone")
	}
	if _, response := postJSON((&Service{}).SetEthernet, `{"enabled":false,"mode":"dhcp"}`); response.Code != -3 {
		t.Fatalf("a change started during a revert: %+v", response)
	}
	if f.read(t, ethernetDisabledFile) != "<absent>" {
		t.Fatal("the refused change was written")
	}
	close(release)
	waitFor(t, "the revert to end", func() bool {
		trialMu.Lock()
		defer trialMu.Unlock()
		return active == nil
	})
	setEthernetOnTrial(t, `{"enabled":false,"mode":"dhcp"}`)
	if f.read(t, trialFile) == "<absent>" {
		t.Fatal("the next change has no record")
	}
}

func TestAChangeThatCannotBeWrittenIsUndoneWithoutARestart(t *testing.T) {
	f := newTrialFixture(t)
	// Turning Ethernet off cannot be written: its directory is read-only.
	readOnly := filepath.Join(f.dir, "read-only")
	if err := os.Mkdir(readOnly, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o700) })
	ethernetDisabledFile = filepath.Join(readOnly, "eth.disabled")
	if _, response := postJSON((&Service{}).SetEthernet, `{"enabled":false,"mode":"dhcp"}`); response.Code != -2 {
		t.Fatalf("a failed write was reported as %+v", response)
	}
	if got := f.read(t, ethernetConfigFile); got != "192.168.1.50/24 192.168.1.1\n" {
		t.Fatalf("config not restored: %q", got)
	}
	if f.read(t, trialFile) != "<absent>" {
		t.Fatal("record left")
	}
	time.Sleep(50 * time.Millisecond)
	if f.restarted.Load() != 0 {
		t.Fatal("the interface was restarted for a change never applied")
	}
}

func TestARestoreThatFailsKeepsTheRecord(t *testing.T) {
	f := newTrialFixture(t)
	previous := "192.168.7.7/24 none\n"
	// The config file's directory is gone and cannot be made: a file is in the way.
	blocker := filepath.Join(f.dir, "gone")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ethernetConfigFile = filepath.Join(blocker, "eth.nodhcp")
	record, _ := json.Marshal(trialRecord{Service: "ethernet", Files: map[string]*string{ethernetConfigFile: &previous}})
	if err := os.WriteFile(trialFile, record, 0o600); err != nil {
		t.Fatal(err)
	}
	RestorePendingTrial()
	waitFor(t, "the restart", func() bool { return f.restarted.Load() == 1 })
	if f.read(t, trialFile) == "<absent>" {
		t.Fatal("the record was removed although the restore failed")
	}
}

func TestAnUnusableRecordIsKeptAsideLoudly(t *testing.T) {
	f := newTrialFixture(t)
	if err := os.WriteFile(trialFile, []byte(`{"files":`), 0o600); err != nil {
		t.Fatal(err)
	}
	RestorePendingTrial()
	if f.read(t, trialFile) != "<absent>" || f.read(t, trialFile+".bad") != `{"files":` {
		t.Fatal("a torn record was not moved aside")
	}
	_ = os.Remove(trialFile + ".bad")
	if f.restarted.Load() != 0 {
		t.Fatal("a torn record restarted the interface")
	}
}
