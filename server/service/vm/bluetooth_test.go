package vm

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// A fake system: /sys/module, /lib/modules and a command log.
func bluetoothFixture(t *testing.T) *[]string {
	t.Helper()
	oldConf, oldSys, oldMods, oldHci := bluetoothConf, bluetoothSysModule, bluetoothModules, bluetoothHci
	oldRun, oldVersion, oldRelease := bluetoothRun, bluetoothVersion, bluetoothRelease
	root := t.TempDir()
	bluetoothConf = filepath.Join(root, "etc/modprobe.d/nanokvm-bluetooth.conf")
	bluetoothSysModule = filepath.Join(root, "sys/module")
	bluetoothModules = filepath.Join(root, "lib/modules")
	bluetoothHci = filepath.Join(root, "sys/class/bluetooth/hci0")
	bluetoothRelease = func() (string, error) { return "7.2.10-test", nil }
	bluetoothVersion = func() (string, error) { return "5.2", nil }
	var log []string
	bluetoothRun = func(name string, args ...string) error {
		cmd := strings.Join(append([]string{name}, args...), " ")
		log = append(log, cmd)
		switch cmd {
		case "modprobe aic8800_btsdio":
			mkdir(t, filepath.Join(bluetoothSysModule, "aic8800_btsdio"))
			mkdir(t, filepath.Join(bluetoothSysModule, "bluetooth"))
		case "rmmod aic8800_btsdio":
			os.RemoveAll(filepath.Join(bluetoothSysModule, "aic8800_btsdio"))
		case "modprobe -r bluetooth":
			os.RemoveAll(filepath.Join(bluetoothSysModule, "bluetooth"))
		}
		return nil
	}
	t.Cleanup(func() {
		bluetoothConf, bluetoothSysModule, bluetoothModules, bluetoothHci = oldConf, oldSys, oldMods, oldHci
		bluetoothRun, bluetoothVersion, bluetoothRelease = oldRun, oldVersion, oldRelease
	})
	mkdir(t, filepath.Join(bluetoothSysModule, "aic8800_fdrv"))
	mkdir(t, filepath.Join(bluetoothModules, "7.2.10-test/extra"))
	write(t, filepath.Join(bluetoothModules, "7.2.10-test/extra/aic8800_btsdio.ko"), "")
	return &log
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, path, data string) {
	t.Helper()
	mkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestBluetoothOffByDefault(t *testing.T) {
	bluetoothFixture(t)
	state := bluetoothState()
	// Off: no version, as the controller cannot be asked.
	if !state.Available || state.Enabled || state.Loaded || state.Version != "" {
		t.Fatalf("state=%+v", state)
	}
}

func TestBluetoothOnOff(t *testing.T) {
	log := bluetoothFixture(t)
	if err := applyBluetooth(true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(bluetoothConf)
	if err != nil || !strings.HasPrefix(string(data), bluetoothConfMarker) ||
		!strings.Contains(string(data), "softdep aic8800_fdrv post: aic8800_btsdio\n") {
		t.Fatalf("conf=%q err=%v", data, err)
	}
	if state := bluetoothState(); !state.Enabled || !state.Loaded || state.Version != "5.2" {
		t.Fatalf("on: state=%+v", state)
	}
	// A controller that does not answer: no version rather than a guess.
	bluetoothVersion = func() (string, error) { return "", os.ErrDeadlineExceeded }
	if state := bluetoothState(); state.Version != "" {
		t.Fatalf("version without an answer: %+v", state)
	}
	bluetoothVersion = func() (string, error) { return "5.2", nil }
	// On again: no second load.
	if err := applyBluetooth(true); err != nil {
		t.Fatal(err)
	}
	if err := applyBluetooth(false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bluetoothConf); !os.IsNotExist(err) {
		t.Fatalf("conf left behind: %v", err)
	}
	// Never "modprobe -r aic8800_btsdio": it would take aic8800_fdrv with it.
	want := []string{"modprobe aic8800_btsdio", "rmmod aic8800_btsdio", "modprobe -r bluetooth"}
	if !reflect.DeepEqual(*log, want) {
		t.Fatalf("commands=%q", *log)
	}
	if state := bluetoothState(); state.Enabled || state.Loaded {
		t.Fatalf("off: state=%+v", state)
	}
}

// Turning on waits for hci0 to answer, so the next state has the version,
// but never fails or hangs because of it.
func TestBluetoothOnWaitsForController(t *testing.T) {
	bluetoothFixture(t)
	old := bluetoothReadyTimeout
	t.Cleanup(func() { bluetoothReadyTimeout = old })
	calls := 0
	bluetoothVersion = func() (string, error) {
		calls++
		if calls < 3 {
			return "", os.ErrDeadlineExceeded
		}
		return "5.2", nil
	}
	if err := applyBluetooth(true); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("asked %d times, want 3", calls)
	}
	if err := applyBluetooth(false); err != nil {
		t.Fatal(err)
	}
	bluetoothReadyTimeout = 600 * time.Millisecond
	bluetoothVersion = func() (string, error) { return "", os.ErrDeadlineExceeded }
	start := time.Now()
	if err := applyBluetooth(true); err != nil {
		t.Fatalf("a mute controller failed the switch: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("waited %s", d)
	}
}

func TestBluetoothLeavesForeignConf(t *testing.T) {
	log := bluetoothFixture(t)
	write(t, bluetoothConf, "softdep aic8800_fdrv post: something\n")
	if err := applyBluetooth(true); err == nil {
		t.Fatal("overwrote a foreign file")
	}
	if err := applyBluetooth(false); err == nil {
		t.Fatal("removed a foreign file")
	}
	if data, _ := os.ReadFile(bluetoothConf); string(data) != "softdep aic8800_fdrv post: something\n" {
		t.Fatalf("conf changed: %q", data)
	}
	if len(*log) != 0 {
		t.Fatalf("ran %q", *log)
	}
	if bluetoothState().Enabled {
		t.Fatal("a foreign file counts as enabled")
	}
}

func TestBluetoothUnavailable(t *testing.T) {
	bluetoothFixture(t)
	os.RemoveAll(filepath.Join(bluetoothSysModule, "aic8800_fdrv"))
	if bluetoothAvailable() {
		t.Fatal("available without the AIC Wi-Fi driver")
	}
	mkdir(t, filepath.Join(bluetoothSysModule, "aic8800_fdrv"))
	os.Remove(filepath.Join(bluetoothModules, "7.2.10-test/extra/aic8800_btsdio.ko"))
	if bluetoothAvailable() {
		t.Fatal("available without the HCI module")
	}
}

func TestBluetoothCoreVersion(t *testing.T) {
	for hci, want := range map[uint8]string{9: "5.0", 11: "5.2", 13: "5.4", 14: "6.0", 99: "HCI 99"} {
		if got := bluetoothCoreVersion(hci); got != want {
			t.Errorf("%d: %q, want %q", hci, got, want)
		}
	}
}

func TestBluetoothReadInfoReply(t *testing.T) {
	ev := func(code, index, opcode uint16, status byte, params ...byte) []byte {
		b := make([]byte, 9, 9+len(params))
		binary.LittleEndian.PutUint16(b[0:], code)
		binary.LittleEndian.PutUint16(b[2:], index)
		binary.LittleEndian.PutUint16(b[4:], uint16(3+len(params)))
		binary.LittleEndian.PutUint16(b[6:], opcode)
		b[8] = status
		return append(b, params...)
	}
	reply := ev(1, 0, 4, 0, 1, 2, 3, 4, 5, 6, 11, 0x60, 0)
	if v, done, err := parseBluetoothReadInfo(reply, 0); !done || err != nil || v != "5.2" {
		t.Fatalf("reply: %q %v %v", v, done, err)
	}
	// Other indexes, other commands and other events are skipped.
	for _, other := range [][]byte{ev(1, 1, 4, 0, make([]byte, 7)...), ev(1, 0, 5, 0, make([]byte, 7)...), ev(6, 0, 4, 0), {1, 0}} {
		if _, done, _ := parseBluetoothReadInfo(other, 0); done {
			t.Fatalf("took %v", other)
		}
	}
	if _, done, err := parseBluetoothReadInfo(ev(2, 0, 4, 0x11), 0); !done || err == nil {
		t.Fatal("status error not reported")
	}
	if _, done, err := parseBluetoothReadInfo(ev(1, 0, 4, 0, 1, 2), 0); !done || err == nil {
		t.Fatal("short reply accepted")
	}
}
