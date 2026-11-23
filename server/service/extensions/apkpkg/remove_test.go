package apkpkg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// RemoveAfter decides from the installed database, under the package lock.
// A package it records is removed even with its files missing, and then
// runs after a removal only once the database shows it gone. A package it
// does not record is not removed, and then runs at once. A held lock, an
// unreadable database or a simulation that keeps the package stops nothing
// and runs nothing; a removal apk does not complete is undone.
func TestRemoveAfterDecidesFromTheDatabase(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "lock")
	saved := []any{lockPackages, installed, apk}
	t.Cleanup(func() {
		lockPackages = saved[0].(func() (*os.File, error))
		installed = saved[1].(func(...string) ([]string, error))
		apk = saved[2].(func(context.Context, ...string) ([]byte, error))
	})
	const name = "nanokvm-zerotier"
	purge := "(1/1) Purging nanokvm-zerotier (1.16.2-r1)\nOK: 130.6 MiB in 174 packages\n"
	keep := "World updated, but the following packages are not removed due to:\n  nanokvm-zerotier: example\n"
	unreadable := errors.New("exit status 99")
	for _, c := range []struct {
		what       string
		locked     bool
		before     []string
		beforeErr  error
		simulation string
		after      []string
		afterErr   error
		steps      string // p: prepared, d: deleted, u: undone, t: then
		removed    bool
		fails      string
	}{
		{"removed", false, []string{name}, nil, purge, nil, nil, "pdt", true, ""},
		{"not recorded", false, nil, nil, "", nil, nil, "t", false, ""},
		{"lock held", true, nil, nil, "", nil, nil, "", false, "in progress"},
		{"database unreadable first", false, nil, unreadable, "", nil, nil, "", false, "cannot read"},
		{"kept by the simulation", false, []string{name}, nil, keep, nil, nil, "", false, "would not remove"},
		{"kept by the removal", false, []string{name}, nil, purge, []string{name}, nil, "pdu", false, "left nanokvm-zerotier installed"},
		{"database unreadable after", false, []string{name}, nil, purge, nil, unreadable, "pdu", false, "cannot confirm"},
	} {
		steps := ""
		lockPackages = func() (*os.File, error) {
			if c.locked {
				return nil, errors.New("another update operation is in progress")
			}
			return os.Create(lock)
		}
		reads := 0
		installed = func(...string) ([]string, error) {
			if reads++; reads == 1 {
				return c.before, c.beforeErr
			}
			return c.after, c.afterErr
		}
		apk = func(_ context.Context, args ...string) ([]byte, error) {
			if args[1] == "--simulate" {
				return []byte(c.simulation), nil
			}
			steps += "d"
			return []byte("OK\n"), nil
		}
		removed, err := RemoveAfter(func() (func() error, error) {
			steps += "p"
			return func() error { steps += "u"; return nil }, nil
		}, func() error { steps += "t"; return nil }, name)
		if steps != c.steps || removed != c.removed || (err == nil) != (c.fails == "") ||
			(err != nil && !strings.Contains(err.Error(), c.fails)) {
			t.Errorf("%s: steps %q, removed %v, error %v", c.what, steps, removed, err)
		}
	}
}
