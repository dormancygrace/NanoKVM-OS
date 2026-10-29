package network

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	// The last chosen network is not the connected one after wpa_supplicant
	// moved to another saved network: current is the connected one.
	if err := writePrivateFile(filepath.Join(w.etc, "wifi.ssid"), []byte("Office")); err != nil {
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
	networks, err := w.readSaved(savedName("Cafe"))
	if err != nil || len(networks) != 2 || networks[0].Ssid != "Cafe" || !networks[0].Current || networks[1].Current {
		t.Fatalf("saved networks %+v, %v", networks, err)
	}
	if networks, _ := w.readSaved(""); networks[0].Current || networks[1].Current {
		t.Fatal("a network is current while not connected")
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
	networks, err := w.readSaved("")
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

// fakeSupplicant answers LIST_NETWORKS with blocks of the given SSIDs as
// wpa_supplicant shows them (printf_encode), GET_NETWORK id_str with the
// block's id_str (FAIL for none, as for a block of an older S30wifi), and
// records REMOVE_NETWORK.
type fakeBlock struct{ listed, idStr string }

func fakeSupplicant(blocks []fakeBlock, status string, removed *[]string) func(string) (string, error) {
	return func(command string) (string, error) {
		switch {
		case command == "STATUS":
			return status, nil
		case command == "LIST_NETWORKS":
			out := "network id / ssid / bssid / flags\n"
			for i, b := range blocks {
				out += fmt.Sprintf("%d\t%s\tany\t\n", i, b.listed)
			}
			return out, nil
		case strings.HasPrefix(command, "GET_NETWORK "):
			fields := strings.Fields(command)
			i, err := strconv.Atoi(fields[1])
			if err != nil || i >= len(blocks) || fields[2] != "id_str" || blocks[i].idStr == "" {
				return "FAIL\n", nil
			}
			return `"` + blocks[i].idStr + `"` + "\n", nil
		case strings.HasPrefix(command, "REMOVE_NETWORK "):
			*removed = append(*removed, strings.TrimPrefix(command, "REMOVE_NETWORK "))
			return "OK\n", nil
		}
		return "FAIL\n", nil
	}
}

func TestRemoveRunningNetworkRemovesOnlyThatSSID(t *testing.T) {
	var removed []string
	w := radioControl{request: fakeSupplicant([]fakeBlock{{"Office", ""}, {"Office", ""}, {`Cafe\x20Bar`, ""}}, "", &removed)}
	w.removeRunningNetwork("Cafe Bar")
	if strings.Join(removed, ",") != "2" {
		t.Fatalf("removed %q", removed)
	}
}

// LIST_NETWORKS escapes quotes, backslashes and tabs as well as other bytes:
// the literal SSIDs Cafe\A and Cafe\x41 are listed as Cafe\\A and Cafe\\x41
// and are different networks. Forgetting one removes it and only it, with
// and without an id_str on the blocks.
func TestRemoveRunningNetworkEscapedSSIDs(t *testing.T) {
	ssids := []string{`Cafe\A`, `Cafe\x41`, `CafeA`, `Say "hi"`, "tab\there"}
	listed := []string{`Cafe\\A`, `Cafe\\x41`, `CafeA`, `Say \"hi\"`, `tab\there`}
	for _, withID := range []bool{false, true} {
		blocks := make([]fakeBlock, len(ssids))
		for i := range ssids {
			blocks[i].listed = listed[i]
			if withID {
				blocks[i].idStr = savedName(ssids[i])
			}
		}
		for i, ssid := range ssids {
			var removed []string
			w := radioControl{request: fakeSupplicant(blocks, "", &removed)}
			w.removeRunningNetwork(ssid)
			if strings.Join(removed, ",") != strconv.Itoa(i) {
				t.Fatalf("id_str %v: forgetting %q removed %q, want %d", withID, ssid, removed, i)
			}
		}
	}
	// A malformed listing or an id_str that is not a saved name matches nothing.
	var removed []string
	w := radioControl{request: fakeSupplicant([]fakeBlock{{`Cafe\q`, ""}, {"Cafe", "4361666"}, {"x\tCafe", ""}}, "", &removed)}
	w.removeRunningNetwork("Cafe")
	if len(removed) != 0 {
		t.Fatalf("removed %q", removed)
	}
}

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
func TestAssociatedNetwork(t *testing.T) {
	var removed []string
	for status, want := range map[string]string{
		"bssid=00:11:22:33:44:55\nssid=Cafe\\x41\nid=3\nid_str=" + savedName(`Cafe\x41`) + "\nwpa_state=COMPLETED\n": savedName(`Cafe\x41`),
		"ssid=Say \\\"hi\\\"\nwpa_state=COMPLETED\n":                                                                 savedName(`Say "hi"`),
		"ssid=Office\nid_str=" + savedName("Office") + "\nwpa_state=SCANNING\n":                                      "",
		"wpa_state=DISCONNECTED\n": "",
	} {
		w := radioControl{request: fakeSupplicant(nil, status, &removed)}
		got, ok := w.associated()
		if got != want || ok != (want != "") {
			t.Errorf("STATUS %q: associated %q, %v; want %q", status, got, ok, want)
		}
	}
}

func TestProfileAutoConnectDefaultsOn(t *testing.T) {
	off := false
	if !(wifiProfile{}).autoConnect() || (wifiProfile{AutoConnect: &off}).autoConnect() {
		t.Fatal("autoConnect: absent must mean on, false must mean off")
	}
}
