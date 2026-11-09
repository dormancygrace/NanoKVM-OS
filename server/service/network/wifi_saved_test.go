package network

import (
	"os"
	"path/filepath"

	"testing"
)

func savedControl(t *testing.T) radioControl {
	t.Helper()
	dir := t.TempDir()
	return radioControl{etc: filepath.Join(dir, "etc"), runDir: filepath.Join(dir, "run")}
}

func TestSaveNetworkReplacesBySSIDAndHidesPasswords(t *testing.T) {
	w := savedControl(t)
	if err := os.MkdirAll(w.etc, 0o755); err != nil {
		t.Fatal(err)
	}
	ssid := `Cafe "Wi-Fi" / 5G`
	if err := w.saveNetwork(wifiProfile{Ssid: ssid, Password: "first-pass", Security: "wpa2"}, true); err != nil {
		t.Fatal(err)
	}
	if err := w.saveNetwork(wifiProfile{Ssid: ssid, Password: "second-pass", Security: "wpa2", Hidden: true}, false); err != nil {
		t.Fatal(err)
	}
	networks, err := w.readSaved("")
	if err != nil || len(networks) != 1 {
		t.Fatalf("saved networks %+v, %v; want one", networks, err)
	}
	n := networks[0]
	if n.Ssid != ssid || n.Security != "wpa2" || !n.Hidden || n.AutoConnect || n.Current {
		t.Fatalf("saved network %+v", n)
	}
	p, auto, err := w.savedProfile(ssid)
	if err != nil || p.Password != "second-pass" || auto {
		t.Fatalf("saved profile %+v auto=%v err=%v", p, auto, err)
	}
	dir := filepath.Join(w.etc, wifiSavedDir)
	for _, path := range []string{dir, w.savedPath(ssid)} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode %v, %v", path, info.Mode(), err)
		}
	}
	if info, err := os.Stat(filepath.Join(w.savedPath(ssid), "pass")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("password mode %v, %v", info.Mode(), err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("saved directory holds %d entries, want 1 (no temporary ones)", len(entries))
	}
}

// The last chosen network is not the connected one after wpa_supplicant
// moved to another saved network: current is the connected one.

// A directory whose name is not its SSID is not one the server wrote.

// The saved set is the source of truth once it exists.

// A password file written with an editor or through /boot ends with a line
// break; the saved network must still pass validation when joined.

// fakeSupplicant answers LIST_NETWORKS with blocks of the given SSIDs as
// wpa_supplicant shows them (printf_encode), GET_NETWORK id_str with the
// block's id_str (FAIL for none, as for a block of an older S30wifi), and
// records REMOVE_NETWORK.

// LIST_NETWORKS escapes quotes, backslashes and tabs as well as other bytes:
// the literal SSIDs Cafe\A and Cafe\x41 are listed as Cafe\\A and Cafe\\x41
// and are different networks. Forgetting one removes it and only it, with
// and without an id_str on the blocks.

// A malformed listing or an id_str that is not a saved name matches nothing.

func TestWpaUnescape(t *testing.T) {
	for value, want := range map[string]string{
		`Office`: "Office", `Cafe\\A`: `Cafe\A`, `Cafe\\x41`: `Cafe\x41`, `Cafe\x41`: "CafeA",
		`Say \"hi\"`: `Say "hi"`, `a\tb\nc\rd\e`: "a\tb\nc\rd\x1b", `\xd0\x9a`: "К",
	} {
		if got, ok := wpaUnescape(value); !ok || got != want {
			t.Errorf("wpaUnescape(%q) = %q, %v; want %q", value, got, ok, want)
		}
	}
	for _, value := range []string{`a\`, `\q`, `\x4`, `\xzz`} {
		if _, ok := wpaUnescape(value); ok {
			t.Errorf("wpaUnescape(%q) accepted", value)
		}
	}
}

// The connected network is named by the block's id_str; a block of an older
// S30wifi by its SSID. Not connected: none.
