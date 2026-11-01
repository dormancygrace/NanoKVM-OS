package timeconfig

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
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
