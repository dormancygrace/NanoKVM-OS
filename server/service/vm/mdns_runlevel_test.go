package vm

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func mdnsFixture(t *testing.T, disabled, inRunlevel bool) *[]string {
	t.Helper()
	dir := t.TempDir()
	oldMarker, oldRunlevel, oldRun := mdnsMarker, mdnsRunlevel, mdnsRun
	mdnsMarker = filepath.Join(dir, "mdns_disabled")
	mdnsRunlevel = filepath.Join(dir, "nanokvm-mdns")
	t.Cleanup(func() { mdnsMarker, mdnsRunlevel, mdnsRun = oldMarker, oldRunlevel, oldRun })
	if disabled {
		if err := os.WriteFile(mdnsMarker, []byte("1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if inRunlevel {
		if err := os.Symlink("/etc/init.d/nanokvm-mdns", mdnsRunlevel); err != nil {
			t.Fatal(err)
		}
	}
	calls := &[]string{}
	mdnsRun = func(argv ...string) error {
		// Unprotected wraps the command in sh -c; record what it runs.
		if argv[0] == "/bin/sh" {
			argv = argv[3:]
		}
		*calls = append(*calls, strings.Join(argv, " "))
		return nil
	}
	return calls
}

func TestReconcileMdns(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		disabled, inRunlevel bool
		want                 []string
	}{
		{"off leaves the runlevel", true, true, []string{"rc-update del nanokvm-mdns default"}},
		{"off and already out", true, false, nil},
		{"on and already in", false, true, nil},
		// A recovery restored the setting but not the runlevel.
		{"on but missing", false, false, []string{"rc-update add nanokvm-mdns default", "rc-service nanokvm-mdns start"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := mdnsFixture(t, tc.disabled, tc.inRunlevel)
			ReconcileMdns()
			if !reflect.DeepEqual(*calls, tc.want) && !(len(*calls) == 0 && len(tc.want) == 0) {
				t.Fatalf("calls %q, want %q", *calls, tc.want)
			}
		})
	}
}

func TestMdnsSwitchMovesTheService(t *testing.T) {
	calls := mdnsFixture(t, false, true)
	if err := syncMdnsRunlevel(false); err != nil {
		t.Fatal(err)
	}
	if want := []string{"rc-update del nanokvm-mdns default"}; !reflect.DeepEqual(*calls, want) {
		t.Fatalf("calls %q, want %q", *calls, want)
	}
	*calls = nil
	os.Remove(mdnsRunlevel)
	if err := syncMdnsRunlevel(true); err != nil {
		t.Fatal(err)
	}
	if want := []string{"rc-update add nanokvm-mdns default"}; !reflect.DeepEqual(*calls, want) {
		t.Fatalf("calls %q, want %q", *calls, want)
	}
}
