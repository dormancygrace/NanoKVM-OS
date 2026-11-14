package wireguard

import (
	"errors"
	"os"
	"testing"
	"time"
)

// Nothing without profiles; with profiles selected, a busy update lock retried.
func TestKeepTools(t *testing.T) {
	defer func(k func(string) error, i func() bool) { keepPackage, installed = k, i }(keepPackage, installed)
	calls, sleeps, s := 0, 0, &Service{dir: t.TempDir()}
	installed = func() bool { return true }
	keepPackage = func(string) error { calls++; return map[bool]error{true: errors.New("busy")}[calls == 1] }
	s.keepTools(func(time.Duration) { sleeps++ })
	_ = os.WriteFile(s.dir+"/profiles.json", []byte(`[{"id":"nk0123456789"}]`), 0600)
	if s.keepTools(func(time.Duration) { sleeps++ }); calls != 2 || sleeps != 1 {
		t.Errorf("%d selections, %d waits", calls, sleeps)
	}
}
