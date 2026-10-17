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
	good := Config{[]string{"pool.ntp.org", "192.0.2.1", "2001:db8::1"}, "Asia/Jerusalem", "24", "auto"}
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
	for _, format := range []string{"dmy", "mdy", "ymd"} {
		c = good
		c.DateFormat = format
		if err := Validate(c); err != nil {
			t.Errorf("rejected date format %q: %v", format, err)
		}
	}
	for _, format := range []string{"", "dd.mm.yyyy", "DMY"} {
		c = good
		c.DateFormat = format
		if Validate(c) == nil {
			t.Errorf("accepted date format %q", format)
		}
	}
	c = good
	c.Servers = []string{"pool.ntp.org", "POOL.NTP.ORG."}
	if Validate(c) == nil {
		t.Error("duplicate accepted")
	}
}

func TestPersistAndRestartOnlyForServers(t *testing.T) {
	dir := t.TempDir()
	conf := "server old.example iburst\ncmdport 0\ndriftfile /var/lib/chrony/drift\n"
	os.WriteFile(filepath.Join(dir, "chrony.conf"), []byte(conf), 0644)
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
	newConf, _ := os.ReadFile(filepath.Join(dir, "chrony.conf"))
	if strings.Contains(string(newConf), "old.example") || !strings.Contains(string(newConf), "cmdport 0") || !strings.Contains(string(newConf), "driftfile") {
		t.Fatal(string(newConf))
	}
}

func TestRestartFailureRollsBack(t *testing.T) {
	dir := t.TempDir()
	old := []byte("server old.example iburst\ncmdport 0\n")
	os.WriteFile(filepath.Join(dir, "chrony.conf"), old, 0644)
	calls := 0
	s := Store{Dir: dir, Restart: func() error {
		calls++
		if calls == 1 {
			return errors.New("failed")
		}
		return nil
	}}
	if s.Save(Config{[]string{"new.example"}, "UTC", "12", "auto"}) == nil {
		t.Fatal("reported success")
	}
	actual, _ := os.ReadFile(filepath.Join(dir, "chrony.conf"))
	if string(actual) != string(old) {
		t.Fatal("chrony configuration was not restored")
	}
	if _, err := os.Stat(s.configPath()); !os.IsNotExist(err) {
		t.Fatal("failed config persisted")
	}
	c, err := s.Read()
	if err != nil || c.Format != "24" || c.Servers[0] != "old.example" || calls != 2 {
		t.Fatal(c, err, calls)
	}
}
