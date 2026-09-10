package hid

import (
	"os"
	"path/filepath"
	"testing"
)

func TestModeUsesConfiguredFunctions(t *testing.T) {
	for _, tt := range []struct {
		name, revision, function, want string
	}{
		{"normal without optional functions", "0x0510", "", ModeNormal},
		{"normal serial", "0x0511", "acm.GS0", ModeNormal},
		{"hid only", "0x0623", "hid.GS0", ModeHidOnly},
		{"hid serial", "0x0624", "acm.GS0", ModeHidOnly},
		{"stale hid revision with rndis", "0x0623", "rndis.usb0", ModeNormal},
		{"stale hid serial revision with ncm", "0x0624", "ncm.usb0", ModeNormal},
		{"stale hid revision with disk", "0x0623", "mass_storage.disk0", ModeNormal},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			config := filepath.Join(root, "configs", "c.1")
			if err := os.MkdirAll(config, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "bcdDevice"), []byte(tt.revision+"\n"), 0644); err != nil {
				t.Fatal(err)
			}
			// Merely creating an unlinked function must not change the profile.
			if err := os.MkdirAll(filepath.Join(root, "functions", "rndis.unlinked"), 0755); err != nil {
				t.Fatal(err)
			}
			if tt.function != "" {
				if err := os.MkdirAll(filepath.Join(root, "functions", tt.function), 0755); err != nil {
					t.Fatal(err)
				}
				// Link labels are arbitrary; inspect the target function type.
				if err := os.Symlink("../../functions/"+tt.function, filepath.Join(config, "function-link")); err != nil {
					t.Fatal(err)
				}
			}
			got, err := getMode(root)
			if err != nil || got != tt.want {
				t.Fatalf("getMode = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestModeRejectsIncompleteConfiguration(t *testing.T) {
	root := t.TempDir()
	if _, err := getMode(root); err == nil {
		t.Fatal("missing revision accepted")
	}
	for _, revision := range []string{"invalid", "0x0623"} {
		if err := os.WriteFile(filepath.Join(root, "bcdDevice"), []byte(revision), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := getMode(root); err == nil {
			t.Fatalf("incomplete configuration accepted: %s", revision)
		}
	}
}
