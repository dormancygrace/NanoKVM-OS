package timeconfig

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestValidation(t *testing.T) {
	good := Config{[]string{"pool.ntp.org", "192.0.2.1", "2001:db8::1"}, "Asia/Jerusalem", "24"}
	if err := Validate(good); err != nil {
		t.Fatal(err)
	}
	for _, server := range []string{"-q", "pool.ntp.org\nrestrict default", "foo;reboot", "https://pool.ntp.org", "foo bar", "", "a..b"} {
		c := good
		c.Servers = []string{server}
		if Validate(c) == nil {
			t.Errorf("accepted %q", server)
		}
	}
	for _, tz := range []string{"../../etc/passwd", "/etc/passwd", "Europe/../UTC", "Missing/Zone", "zone.tab"} {
		c := good
		c.Timezone = tz
		if Validate(c) == nil {
			t.Errorf("accepted zone %q", tz)
		}
	}
	c := good
	c.Format = "auto"
	if Validate(c) == nil {
		t.Error("accepted auto")
	}
	c = good
	c.Servers = []string{"pool.ntp.org", "POOL.NTP.ORG."}
	if Validate(c) == nil {
		t.Error("duplicate accepted")
	}
}

func TestPersistAndRestartOnlyForServers(t *testing.T) {
	dir := t.TempDir()
	ntp := "server old.example iburst\nrestrict default nomodify noquery\ndriftfile /var/lib/ntp/ntp.drift\n"
	os.WriteFile(filepath.Join(dir, "ntp.conf"), []byte(ntp), 0644)
	calls := 0
	s := Store{Dir: dir, Restart: func() error { calls++; return nil }}
	c, err := s.Read()
	if err != nil || c.Format != "24" {
		t.Fatal(c, err)
	}
	c.Timezone = "Asia/Jerusalem"
	if err = s.Save(c); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("timezone change restarted NTP")
	}
	bytes, _ := os.ReadFile(filepath.Join(dir, "localtime"))
	loc, err := time.LoadLocationFromTZData(c.Timezone, bytes)
	if err != nil {
		t.Fatal(err)
	}
	_, winter := time.Date(2026, 1, 15, 12, 0, 0, 0, loc).Zone()
	_, summer := time.Date(2026, 7, 15, 12, 0, 0, 0, loc).Zone()
	if winter != 7200 || summer != 10800 {
		t.Fatal("DST rules not installed")
	}
	c.Servers = []string{"new.example", "192.0.2.123"}
	c.Format = "12"
	if err = s.Save(c); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("NTP server update did not restart once")
	}
	actual, err := (&Store{Dir: dir}).Read()
	if err != nil || !reflect.DeepEqual(actual, c) {
		t.Fatal(actual, err)
	}
	newNTP, _ := os.ReadFile(filepath.Join(dir, "ntp.conf"))
	if strings.Contains(string(newNTP), "old.example") || !strings.Contains(string(newNTP), "restrict default nomodify noquery") || !strings.Contains(string(newNTP), "driftfile") {
		t.Fatal(string(newNTP))
	}
}

func TestRestartFailureRollsBack(t *testing.T) {
	dir := t.TempDir()
	old := []byte("server old.example iburst\nrestrict default noquery\n")
	os.WriteFile(filepath.Join(dir, "ntp.conf"), old, 0644)
	calls := 0
	s := Store{Dir: dir, Restart: func() error {
		calls++
		if calls == 1 {
			return errors.New("failed")
		}
		return nil
	}}
	if s.Save(Config{[]string{"new.example"}, "UTC", "12"}) == nil {
		t.Fatal("reported success")
	}
	actual, _ := os.ReadFile(filepath.Join(dir, "ntp.conf"))
	if string(actual) != string(old) {
		t.Fatal("NTP was not restored")
	}
	if _, err := os.Stat(s.configPath()); !os.IsNotExist(err) {
		t.Fatal("failed config persisted")
	}
	c, err := s.Read()
	if err != nil || c.Format != "24" || c.Servers[0] != "old.example" || calls != 2 {
		t.Fatal(c, err, calls)
	}
}
