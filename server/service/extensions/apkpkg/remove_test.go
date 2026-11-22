package apkpkg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// RemoveAfter undoes the caller's preparation unless the installed database
// shows the package gone: when apk kept it and when the database could not be
// read. A simulation that keeps the package stops nothing.
func TestRemoveAfterUndoesUnlessProvenRemoved(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "lock")
	saved := []any{lockPackages, installed, apk}
	t.Cleanup(func() {
		lockPackages = saved[0].(func() (*os.File, error))
		installed = saved[1].(func(...string) ([]string, error))
		apk = saved[2].(func(context.Context, ...string) ([]byte, error))
	})
	lockPackages = func() (*os.File, error) { return os.Create(lock) }
	const name = "nanokvm-zerotier"
	purge := "(1/1) Purging nanokvm-zerotier (1.16.2-r1)\nOK: 130.6 MiB in 174 packages\n"
	keep := "World updated, but the following packages are not removed due to:\n  nanokvm-zerotier: example\n"
	for _, c := range []struct {
		what       string
		simulation string
		left       []string
		checkErr   error
		prepared   bool
		undone     bool
		fails      string
	}{
		{"removed", purge, nil, nil, true, false, ""},
		{"kept by the simulation", keep, nil, nil, false, false, "would not remove"},
		{"kept by the removal", purge, []string{name}, nil, true, true, "left nanokvm-zerotier installed"},
		{"unreadable database", purge, nil, errors.New("exit status 99"), true, true, "cannot confirm"},
	} {
		apk = func(_ context.Context, args ...string) ([]byte, error) {
			if args[1] == "--simulate" {
				return []byte(c.simulation), nil
			}
			return []byte("OK\n"), nil
		}
		installed = func(...string) ([]string, error) { return c.left, c.checkErr }
		prepared, undone := false, false
		err := RemoveAfter(func() (func() error, error) {
			prepared = true
			return func() error { undone = true; return nil }, nil
		}, name)
		if prepared != c.prepared || undone != c.undone || (err == nil) != (c.fails == "") ||
			(err != nil && !strings.Contains(err.Error(), c.fails)) {
			t.Errorf("%s: prepared %v, undone %v, error %v", c.what, prepared, undone, err)
		}
	}
}
