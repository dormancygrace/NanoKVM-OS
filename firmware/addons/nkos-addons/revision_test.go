package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublishedDescriptorRevisionMetadata(t *testing.T) {
	const published = `{"schema":1,"id":"hello","package":"nkos-addon-hello","source_version":"1.0.0","pkgver":"1.0.0","pkgrel":10,"version":"1.0.0-r10","arch":"riscv64","base_abi":"nkos-base-abi=1.0.0","server_api":"nkos-server-api=1","features":["nkos-feature-shell=1","nkos-feature-static-riscv64=1"],"config":"/etc/kvm/hello","data":"/data/hello","services":[],"preserve":["config","data"]}`
	for _, tc := range []struct {
		name, descriptor string
		valid            bool
	}{
		{"published", published, true},
		{"revision-zero", strings.ReplaceAll(published, "10", "0"), true},
		{"mismatched-revision", strings.Replace(published, `"pkgrel":10`, `"pkgrel":1`, 1), false},
		{"negative-revision", strings.Replace(published, `"pkgrel":10`, `"pkgrel":-1`, 1), false},
		{"missing-revision", strings.Replace(published, `"pkgrel":10,`, "", 1), false},
		{"unknown-field", strings.Replace(published, `"schema":1`, `"schema":1,"surprise":true`, 1), false},
		{"legacy", strings.Replace(published, `"source_version":"1.0.0","pkgver":"1.0.0","pkgrel":10,`, "", 1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "addons", "hello", "addon.json")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.descriptor), 0600); err != nil {
				t.Fatal(err)
			}
			m := &manager{root: root}
			_, _, err := m.loadAddon("hello", false, false)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}
