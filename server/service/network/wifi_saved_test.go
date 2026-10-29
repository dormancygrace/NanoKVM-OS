package network

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
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
	networks, err := w.readSaved()
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

func TestReadSavedMarksCurrentAndSkipsForeignEntries(t *testing.T) {
	w := savedControl(t)
	if err := os.MkdirAll(w.etc, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, ssid := range []string{"Office", "Cafe"} {
		if err := w.saveNetwork(wifiProfile{Ssid: ssid, Password: "password1", Security: "personal"}, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := writePrivateFile(filepath.Join(w.etc, "wifi.ssid"), []byte("Cafe")); err != nil {
		t.Fatal(err)
	}
	// A directory whose name is not its SSID is not one the server wrote.
	foreign := filepath.Join(w.etc, wifiSavedDir, savedName("Elsewhere"))
	if err := os.Mkdir(foreign, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreign, "ssid"), []byte("Other"), 0o600); err != nil {
		t.Fatal(err)
	}
	networks, err := w.readSaved()
	if err != nil || len(networks) != 2 || networks[0].Ssid != "Cafe" || !networks[0].Current || networks[1].Current {
		t.Fatalf("saved networks %+v, %v", networks, err)
	}
	if _, _, err := w.savedProfile("Other"); err == nil {
		t.Fatal("an entry under another SSID's name was read")
	}
}

func TestSaveNetworkKeepsTheMostRecent(t *testing.T) {
	w := savedControl(t)
	if err := os.MkdirAll(w.etc, 0o755); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-time.Hour)
	for i := 0; i < maxSavedNetworks; i++ {
		ssid := fmt.Sprintf("net-%02d", i)
		if err := w.saveNetwork(wifiProfile{Ssid: ssid, Password: "password1", Security: "personal"}, true); err != nil {
			t.Fatal(err)
		}
		stamp := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(w.savedPath(ssid), stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.saveNetwork(wifiProfile{Ssid: "newest", Password: "password1", Security: "personal"}, true); err != nil {
		t.Fatal(err)
	}
	networks, err := w.readSaved()
	if err != nil || len(networks) != maxSavedNetworks {
		t.Fatalf("%d saved networks, %v; want %d", len(networks), err, maxSavedNetworks)
	}
	if _, err := os.Stat(w.savedPath("net-00")); !os.IsNotExist(err) {
		t.Fatal("the network joined longest ago was kept")
	}
	if _, err := os.Stat(w.savedPath("newest")); err != nil {
		t.Fatal("the network just saved was removed")
	}
}

func TestMigrateSavedStoresTheSingleNetworkOnce(t *testing.T) {
	w := savedControl(t)
	if err := os.MkdirAll(w.etc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := w.migrateSaved(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(w.etc, wifiSavedDir)); !os.IsNotExist(err) {
		t.Fatal("migration without a network created the saved directory")
	}
	for name, value := range map[string]string{"wifi.ssid": "Office\n", "wifi.pass": "secret123\n", "wifi.security": "wpa3", "wifi.hidden": "true"} {
		if err := writePrivateFile(filepath.Join(w.etc, name), []byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.migrateSaved(); err != nil {
		t.Fatal(err)
	}
	p, auto, err := w.savedProfile("Office")
	if err != nil || !auto || p.Security != "wpa3" || !p.Hidden {
		t.Fatalf("migrated %+v auto=%v err=%v", p, auto, err)
	}
	// The saved set is the source of truth once it exists.
	if err := w.setAutoConnect("Office", false); err != nil {
		t.Fatal(err)
	}
	if err := w.migrateSaved(); err != nil {
		t.Fatal(err)
	}
	if _, auto, _ := w.savedProfile("Office"); auto {
		t.Fatal("a second migration overwrote the saved network")
	}
}

// A password file written with an editor or through /boot ends with a line
// break; the saved network must still pass validation when joined.
func TestMigratedPasswordWithoutLineBreak(t *testing.T) {
	w := savedControl(t)
	if err := os.MkdirAll(w.etc, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"wifi.ssid": "Office\n", "wifi.pass": "secret123\r\n"} {
		if err := writePrivateFile(filepath.Join(w.etc, name), []byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.migrateSaved(); err != nil {
		t.Fatal(err)
	}
	p, _, err := w.savedProfile("Office")
	if err != nil || p.Password != "secret123" || p.validate() != nil {
		t.Fatalf("saved %+v, %v, validate %v", p, err, p.validate())
	}
	stored, _ := os.ReadFile(filepath.Join(w.savedPath("Office"), "pass"))
	if string(stored) != "secret123" {
		t.Fatalf("stored password %q", stored)
	}
}

func TestMigrateSkipsANetworkWithoutPassword(t *testing.T) {
	w := savedControl(t)
	if err := os.MkdirAll(w.etc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateFile(filepath.Join(w.etc, "wifi.ssid"), []byte("Office")); err != nil {
		t.Fatal(err)
	}
	if err := w.migrateSaved(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(w.etc, wifiSavedDir)); !os.IsNotExist(err) {
		t.Fatal("a network without its password was saved")
	}
}

func TestSelectedNetworkLivesInTheRunDirectory(t *testing.T) {
	w := savedControl(t)
	if err := os.MkdirAll(w.etc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := w.rememberJoined(wifiProfile{Ssid: "Hotel", Password: "password1", Security: "personal"}, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(w.runDir, wifiSelectedFile))
	if err != nil || string(data) != savedName("Hotel") {
		t.Fatalf("selected %q, %v", data, err)
	}
	if err := w.clearSelected(); err != nil {
		t.Fatal(err)
	}
	if err := w.clearSelected(); err != nil {
		t.Fatal("clearing twice failed:", err)
	}
	if _, err := os.Stat(filepath.Join(w.runDir, wifiSelectedFile)); !os.IsNotExist(err) {
		t.Fatal("selection was not cleared")
	}
}

func TestRemoveRunningNetworkRemovesOnlyThatSSID(t *testing.T) {
	var sent []string
	w := radioControl{request: func(command string) (string, error) {
		sent = append(sent, command)
		if command == "LIST_NETWORKS" {
			return "network id / ssid / bssid / flags\n0\tOffice\tany\t[CURRENT]\n1\tOffice\tany\t\n2\tCafe\\x20Bar\tany\t\n", nil
		}
		return "OK\n", nil
	}}
	w.removeRunningNetwork("Cafe Bar")
	if len(sent) != 2 || sent[1] != "REMOVE_NETWORK 2" {
		t.Fatalf("commands %q", sent)
	}
}

func TestProfileAutoConnectDefaultsOn(t *testing.T) {
	off := false
	if !(wifiProfile{}).autoConnect() || (wifiProfile{AutoConnect: &off}).autoConnect() {
		t.Fatal("autoConnect: absent must mean on, false must mean off")
	}
}
