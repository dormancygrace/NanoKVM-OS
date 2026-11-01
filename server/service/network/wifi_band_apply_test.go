package network

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// bandSupplicant fakes wpa_supplicant's control socket for applyPreferredBand.
func bandSupplicant(networks string, priorities map[string]string, replies map[string]string) (*[]string, func(string) (string, error)) {
	var sent []string
	return &sent, func(command string) (string, error) {
		sent = append(sent, command)
		switch {
		case command == "LIST_NETWORKS":
			return networks, nil
		case len(command) > 12 && command[:12] == "GET_NETWORK ":
			id := strings.Fields(command)[1]
			if p, ok := priorities[id]; ok {
				return p, nil
			}
			return "FAIL", nil
		}
		if reply, ok := replies[command]; ok {
			return reply, nil
		}
		return "OK\n", nil
	}
}

const twoBlocks = "network id / ssid / bssid / flags\n0\tOffice\tany\t\n1\tOffice\tany\t[CURRENT]\n"

func bandRadio(t *testing.T, saved map[string]string, request func(string) (string, error)) radioControl {
	t.Helper()
	etc := t.TempDir()
	for name, value := range saved {
		if err := os.WriteFile(filepath.Join(etc, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// The adapter's channels come from "iw phy <phy> info"; the phy is the
	// target of wlan0/phy80211.
	net := filepath.Join(t.TempDir(), "wlan0")
	if err := os.MkdirAll(filepath.Join(filepath.Dir(net), "phy0"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(net, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(filepath.Dir(net), "phy0"), filepath.Join(net, "phy80211")); err != nil {
		t.Fatal(err)
	}
	return radioControl{etc: etc, net: net, request: request,
		run: func(name string, args ...string) (string, error) {
			if name == "iw" {
				return "* 2412 MHz [1]\n* 2437 MHz [6]\n* 5180 MHz [36]", nil
			}
			return "", errors.New("unexpected command " + name)
		}}
}

func TestApplyPreferredBandRetargetsThePreferredBlock(t *testing.T) {
	sent, request := bandSupplicant(twoBlocks, map[string]string{"0": "2", "1": "1"}, nil)
	w := bandRadio(t, map[string]string{"wifi.freq_list_2.4": "2412 2437", "wifi.freq_list_5": "5180"}, request)
	if err := w.applyPreferredBand("2.4"); err != nil {
		t.Fatal(err)
	}
	want := []string{"LIST_NETWORKS", "GET_NETWORK 0 priority", "SET_NETWORK 0 freq_list 2412 2437", "SCAN"}
	if !reflect.DeepEqual(*sent, want) {
		t.Fatalf("sent %q, want %q", *sent, want)
	}
}

// Without a preferred block (a single-band adapter, or a configuration
// written by an older S30wifi) nothing changes until the next start.
func TestApplyPreferredBandWithoutPreferredBlock(t *testing.T) {
	sent, request := bandSupplicant("network id / ssid / bssid / flags\n0\tOffice\tany\t[CURRENT]\n", map[string]string{"0": "1"}, nil)
	w := bandRadio(t, map[string]string{"wifi.freq_list_5": "5180"}, request)
	if err := w.applyPreferredBand("5"); err != nil {
		t.Fatal(err)
	}
	for _, command := range *sent {
		if command == "SCAN" || len(command) > 11 && command[:11] == "SET_NETWORK" {
			t.Fatalf("changed the running configuration: %q", *sent)
		}
	}
}

func TestApplyPreferredBandRejectsInvalidSavedChannels(t *testing.T) {
	sent, request := bandSupplicant(twoBlocks, map[string]string{"0": "2", "1": "1"}, nil)
	w := bandRadio(t, map[string]string{"wifi.freq_list_5": "5180 key_mgmt=NONE"}, request)
	if err := w.applyPreferredBand("5"); err != nil || len(*sent) != 0 {
		t.Fatalf("error %v, sent %q", err, *sent)
	}
}

// Updating the active block does not bypass wpa_supplicant's RSSI/throughput
// roaming heuristic. Explicit reassociation must override it for this request.
func TestApplyPreferredBandReselectsTheActivePreferredBlock(t *testing.T) {
	networks := "network id / ssid / bssid / flags\n12\tOffice\tany\t[CURRENT]\n13\tOffice\tany\t\n"
	sent, request := bandSupplicant(networks, map[string]string{"12": "2", "13": "1"}, nil)
	w := bandRadio(t, map[string]string{"wifi.freq_list_2.4": "2412 2437"}, request)
	if err := w.applyPreferredBand("2.4"); err != nil {
		t.Fatal(err)
	}
	want := []string{"LIST_NETWORKS", "GET_NETWORK 12 priority", "SET_NETWORK 12 freq_list 2412 2437", "REASSOCIATE"}
	if !reflect.DeepEqual(*sent, want) {
		t.Fatalf("sent %q, want %q", *sent, want)
	}
}

func TestApplyPreferredBandRejectsUnsupportedBandAndWrongBandChannels(t *testing.T) {
	for _, tc := range []struct{ band, file, value string }{
		{"5/../rogue", "rogue", "5180"}, {"5", "wifi.freq_list_5", "2412"}, {"5", "wifi.freq_list_5", "-5180"},
	} {
		t.Run(tc.band+tc.value, func(t *testing.T) {
			sent, request := bandSupplicant(twoBlocks, map[string]string{"0": "2", "1": "1"}, nil)
			w := bandRadio(t, map[string]string{tc.file: tc.value}, request)
			if err := w.applyPreferredBand(tc.band); err != nil || len(*sent) != 0 {
				t.Fatalf("error %v, sent %q", err, *sent)
			}
		})
	}
}
