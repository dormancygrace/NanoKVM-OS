package wireguard

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// wireguard-tools is selected while there are profiles and WireGuard is
// installed; a busy update lock is retried.
func TestKeepTools(t *testing.T) {
	defer func(k func(string) error, i func() bool) { keepPackage, installed = k, i }(keepPackage, installed)
	for name, c := range map[string]struct {
		profiles  string
		installed bool
		fails     int
		calls     int
	}{
		"no profiles":         {"", true, 0, 0},
		"profiles":            {`[{"id":"nk0123456789","name":"a"}]`, true, 0, 1},
		"lock busy once":      {`[{"id":"nk0123456789","name":"a"}]`, true, 1, 2},
		"wireguard not there": {`[{"id":"nk0123456789","name":"a"}]`, false, 0, 0},
	} {
		s := &Service{dir: t.TempDir(), failures: map[string]string{}}
		if c.profiles != "" {
			_ = os.WriteFile(filepath.Join(s.dir, "profiles.json"), []byte(c.profiles), 0600)
		}
		calls, sleeps := 0, 0
		installed = func() bool { return c.installed }
		keepPackage = func(name string) error {
			calls++
			if name != "wireguard-tools" || calls <= c.fails {
				return errors.New("another update operation is in progress")
			}
			return nil
		}
		s.keepTools(func(time.Duration) { sleeps++ })
		if calls != c.calls || sleeps != c.fails {
			t.Errorf("%s: %d selections, %d waits", name, calls, sleeps)
		}
	}
}
