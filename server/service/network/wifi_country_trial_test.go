package network

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"NanoKVM-Server/internal/networktrial"

	"github.com/gin-gonic/gin"
)

// regdbFixture builds a regulatory.db listing codes, as db2fw writes it.
func regdbFixture(codes ...string) []byte {
	data := []byte("RGDB")
	data = binary.BigEndian.AppendUint32(data, 20)
	for _, code := range codes {
		data = append(data, code...)
		data = binary.BigEndian.AppendUint16(data, 0x40)
	}
	return append(data, 0, 0, 0, 0)
}

func TestParseRegulatoryDB(t *testing.T) {
	got, err := parseRegulatoryDB(regdbFixture("00", "DE", "US"))
	if err != nil || !reflect.DeepEqual(got, []string{"00", "DE", "US"}) {
		t.Fatalf("got %v %v", got, err)
	}
	// An entry that is not a country code is left out.
	if got, err := parseRegulatoryDB(regdbFixture("DE", "x1")); err != nil || !reflect.DeepEqual(got, []string{"DE"}) {
		t.Fatalf("invalid code: %v %v", got, err)
	}
	wrongVersion := regdbFixture("DE")
	binary.BigEndian.PutUint32(wrongVersion[4:8], 19)
	truncated := regdbFixture("DE")
	truncated = truncated[:len(truncated)-4]
	for name, data := range map[string][]byte{
		"empty": nil, "magic": append([]byte("XXXX"), regdbFixture("DE")[4:]...),
		"version": wrongVersion, "truncated": truncated,
	} {
		if got, err := parseRegulatoryDB(data); err == nil {
			t.Errorf("%s: accepted as %v", name, got)
		}
	}
}

// countryRadio stands in for iw and S30wifi: "iw reg set" puts the country
// in force at once, and a restart can be held.
type countryRadio struct {
	mu       sync.Mutex
	domain   string
	commands []string
	hold     chan struct{}
	entered  chan struct{}
}

func (r *countryRadio) run(name string, args ...string) (string, error) {
	command := strings.Join(append([]string{name}, args...), " ")
	r.mu.Lock()
	r.commands = append(r.commands, command)
	switch {
	case command == "iw reg get":
		domain := r.domain
		r.mu.Unlock()
		return "global\ncountry " + domain + ": DFS-ETSI\n", nil
	case name == "iw" && len(args) == 3 && args[0] == "reg" && args[1] == "set":
		r.domain = args[2]
	case name == WiFiScript:
		hold, entered := r.hold, r.entered
		r.mu.Unlock()
		if hold != nil {
			entered <- struct{}{}
			<-hold
		}
		return "", nil
	}
	r.mu.Unlock()
	return "", nil
}

func (r *countryRadio) ran() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.commands)
}

// newCountryRadio: Wi-Fi on with the country saved and in force, and a
// regulatory database of 00, DE, FR and US.
func newCountryRadio(t *testing.T, f *trialFixture, saved string) *countryRadio {
	t.Helper()
	r := &countryRadio{domain: saved}
	etc, wlan := filepath.Join(f.dir, "etc"), filepath.Join(f.dir, "wlan0")
	for _, dir := range []string{etc, wlan} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	oldControl, oldDB, oldRide, oldPrefix := wifiControl, regulatoryDB, countryRequestMayRideWifi, wifiAPPrefix
	wifiControl = radioControl{etc: etc, net: wlan, run: r.run}
	regulatoryDB = filepath.Join(f.dir, "regulatory.db")
	wifiAPPrefix = filepath.Join(etc, "wifi.ap")
	if err := os.WriteFile(regulatoryDB, regdbFixture("00", "DE", "FR", "US"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := wifiControl.saveCountry(saved); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Nothing may still restart through this radio once it is gone.
		waitTrialIdle(t)
		waitWifiIdle(t)
		wifiControl, regulatoryDB, countryRequestMayRideWifi, wifiAPPrefix = oldControl, oldDB, oldRide, oldPrefix
		wifiMu.Lock()
		wifiBusy, wifiError = false, ""
		wifiMu.Unlock()
	})
	return r
}

func waitWifiIdle(t *testing.T) {
	t.Helper()
	waitFor(t, "Wi-Fi to be released", func() bool {
		wifiMu.Lock()
		defer wifiMu.Unlock()
		return !wifiBusy
	})
}

type countryResponse struct {
	Country   string    `json:"country"`
	Active    string    `json:"active"`
	Supported []string  `json:"supported"`
	Trial     trialInfo `json:"trial"`
}

func setCountry(t *testing.T, country string) (int, countryResponse) {
	t.Helper()
	_, response := postJSON((&Service{}).SetWifiCountry, `{"country":"`+country+`"}`)
	data, _ := json.Marshal(response.Data)
	var got countryResponse
	_ = json.Unmarshal(data, &got)
	return response.Code, got
}

func TestWifiCountryOnlyFromTheRegulatoryDatabase(t *testing.T) {
	f := newTrialFixture(t)
	r := newCountryRadio(t, f, "DE")
	recorder := httptest.NewRecorder()
	router := gin.New()
	router.GET("/", (&Service{}).GetWifiCountry)
	router.ServeHTTP(recorder, httptest.NewRequest("GET", "/", nil))
	var got struct {
		Data countryResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil ||
		!reflect.DeepEqual(got.Data.Supported, []string{"00", "DE", "FR", "US"}) || got.Data.Active != "DE" {
		t.Fatalf("GET: %s", recorder.Body.String())
	}
	// IQ is a valid code that wireless-regdb lacks: iw would take it and
	// leave DE in force.
	if code, _ := setCountry(t, "IQ"); code != -1 {
		t.Fatalf("a country without rules = %d", code)
	}
	if got := wifiControl.country(); got != "DE" || len(r.ran()) != 1 {
		t.Fatalf("saved %q, ran %v", got, r.ran())
	}
	// Without a database to check against, every valid code passes; with
	// Wi-Fi off it is only saved.
	regulatoryDB = filepath.Join(f.dir, "missing.db")
	if err := os.WriteFile(filepath.Join(wifiControl.etc, "wifi.disabled"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _ := setCountry(t, "IQ"); code != 0 || wifiControl.country() != "IQ" {
		t.Fatalf("without a database: %d %q", code, wifiControl.country())
	}
}

// A change on trial (here Ethernet's) or a record left to undo refuses the
// country, also with Wi-Fi off and over Ethernet: the revert would bring back
// settings chosen under the previous country.
func TestWifiCountryWaitsForATrial(t *testing.T) {
	f := newTrialFixture(t)
	newCountryRadio(t, f, "DE")
	setEthernetOnTrial(t, newStatic)
	if code, _ := setCountry(t, "US"); code != -3 {
		t.Fatalf("over Wi-Fi during a trial = %d", code)
	}
	countryRequestMayRideWifi = func(*gin.Context) bool { return false }
	if code, _ := setCountry(t, "US"); code != -3 {
		t.Fatalf("over Ethernet during a trial = %d", code)
	}
	waitApplied(t)
	endTrialForTest()

	if err := os.WriteFile(trialFile, []byte(`{"files":{},"service":"ethernet"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wifiControl.etc, "wifi.disabled"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _ := setCountry(t, "US"); code != -3 {
		t.Fatalf("with a record to undo = %d", code)
	}
	if got := wifiControl.country(); got != "DE" {
		t.Fatalf("a refused country was saved: %q", got)
	}
	_ = os.Remove(trialFile)
	waitWifiIdle(t)
}

// Over Wi-Fi the country runs on trial: unconfirmed, the previous one comes
// back and Wi-Fi restarts with it.
func TestAnUnconfirmedWifiCountryIsUndone(t *testing.T) {
	f := newTrialFixture(t)
	r := newCountryRadio(t, f, "DE")
	trialDuration = time.Hour
	code, got := setCountry(t, "US")
	if code != 0 || got.Trial.Token == "" || got.Country != "US" || got.Active != "DE" {
		t.Fatalf("set = %d %+v", code, got)
	}
	var record trialRecord
	data, _ := os.ReadFile(trialFile)
	if err := json.Unmarshal(data, &record); err != nil || record.Service != "wifi-country" ||
		len(record.Files) != 1 || record.Files[wifiControl.countryPath()] == nil || *record.Files[wifiControl.countryPath()] != "DE\n" {
		t.Fatalf("record %s", data)
	}
	if wifiControl.country() != "US" {
		t.Fatal("not saved")
	}
	// A second change waits for this one, also once Wi-Fi has restarted.
	waitApplied(t)
	waitWifiIdle(t)
	if code, _ := setCountry(t, "FR"); code != -3 {
		t.Fatalf("second change during the trial = %d", code)
	}
	// Not confirmed in time.
	trialMu.Lock()
	pending := active
	trialMu.Unlock()
	revertTrial(pending, "not confirmed in time", true)
	waitTrialIdle(t)
	if f.read(t, trialFile) != "<absent>" {
		t.Fatal("record left")
	}
	if got := wifiControl.country(); got != "DE" {
		t.Fatalf("country not restored: %q", got)
	}
	want := []string{"iw reg get", "iw reg set US", WiFiScript + " restart", "iw reg set DE", WiFiScript + " restart"}
	var applied []string
	for _, command := range r.ran() {
		if command != "iw reg get" || len(applied) == 0 {
			applied = append(applied, command)
		}
	}
	if !reflect.DeepEqual(applied, want) {
		t.Fatalf("commands %v, want %v", applied, want)
	}
}

// The restart that applies a country keeps shutdown and standby waiting,
// also without a trial (over Ethernet).
func TestWifiCountryRestartHoldsShutdown(t *testing.T) {
	f := newTrialFixture(t)
	r := newCountryRadio(t, f, "DE")
	countryRequestMayRideWifi = func(*gin.Context) bool { return false }
	r.hold, r.entered = make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(r.hold) })
	t.Cleanup(release)
	code, got := setCountry(t, "US")
	if code != 0 || got.Trial.Token != "" {
		t.Fatalf("set over Ethernet = %d %+v", code, got)
	}
	<-r.entered
	if _, err := os.Lstat(trialFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a change over Ethernet left a trial record")
	}
	if err := networktrial.CheckIdle(trialFile); !errors.Is(err, networktrial.ErrPending) {
		t.Fatalf("shutdown preflight during the restart: %v", err)
	}
	release()
	waitWifiIdle(t)
	waitFor(t, "the restart to be released", func() bool { return networktrial.CheckIdle(trialFile) == nil })
	if wifiControl.country() != "US" {
		t.Fatal("not saved")
	}
}

// An access point trial records the country with its own settings, and a
// record that names it is restored at start, not set aside.
func TestWifiAPTrialRecordsTheCountry(t *testing.T) {
	f := newTrialFixture(t)
	newCountryRadio(t, f, "DE")
	if err := os.WriteFile(wifiAPFile("band"), []byte("5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	trial, err := beginTrial("wifi-ap", wifiAPTrialFiles())
	if err != nil {
		t.Fatal(err)
	}
	if previous := trial.record.Files[wifiControl.countryPath()]; previous == nil || *previous != "DE\n" {
		t.Fatalf("country not recorded: %v", trial.record.Files)
	}
	data, err := os.ReadFile(trialFile)
	if err != nil {
		t.Fatal(err)
	}
	endTrialForTest()
	// The device restarted during the trial, with other settings saved.
	if err := os.WriteFile(trialFile, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := wifiControl.saveCountry("US"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wifiAPFile("band"), []byte("2.4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	RestorePendingTrial()
	waitTrialIdle(t)
	if f.read(t, trialFile+".bad") != "<absent>" {
		t.Fatal("a record naming the country was set aside")
	}
	if wifiControl.country() != "DE" || f.read(t, wifiAPFile("band")) != "5\n" {
		t.Fatalf("not restored: %q %q", wifiControl.country(), f.read(t, wifiAPFile("band")))
	}
}
