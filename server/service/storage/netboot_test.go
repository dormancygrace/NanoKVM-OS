package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseTagsAreStrict(t *testing.T) {
	for _, tag := range []string{"3.0.3", "10.20.30"} {
		if !netbootxyzTag.MatchString(tag) {
			t.Fatalf("netboot.xyz %q rejected", tag)
		}
	}
	for _, tag := range []string{"", "3.0", "v3.0.3", "3.0.3\nshell", "3.0.3 || shell", "../3.0.3", "3.0.3-RC"} {
		if netbootxyzTag.MatchString(tag) {
			t.Fatalf("netboot.xyz %q accepted", tag)
		}
	}
	for _, tag := range []string{"2.0.0", "v2.0", "v2.0.0-rc1", "v2.0.0/../x"} {
		if ipxeTag.MatchString(tag) {
			t.Fatalf("iPXE %q accepted", tag)
		}
	}
}

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// fakeReleases serves the GitHub API, the iPXE downloads and boot.netboot.xyz.
func fakeReleases(t *testing.T, images map[string][]byte, published map[string]string, xyzTag string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/ipxe/ipxe/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		var assets []map[string]any
		for name, data := range images {
			assets = append(assets, map[string]any{"name": name, "size": len(data), "digest": "sha256:" + published[name]})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v2.0.0", "assets": assets})
	})
	mux.HandleFunc("/repos/netbootxyz/netboot.xyz/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": xyzTag})
	})
	mux.HandleFunc("/download/v2.0.0/", func(w http.ResponseWriter, r *http.Request) {
		data, ok := images[filepath.Base(r.URL.Path)]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	})
	mux.HandleFunc("/xyz/"+xyzTag+"/menu.ipxe", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("#!ipxe\nmenu\n"))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	for _, v := range []*string{&githubAPI, &ipxeDownloadBase, &netbootxyzMenuBase, &netbootDir} {
		old := *v
		t.Cleanup(func() { *v = old })
	}
	githubAPI, ipxeDownloadBase, netbootxyzMenuBase = server.URL, server.URL+"/download", server.URL+"/xyz"
	netbootDir = t.TempDir()
}

func TestUpdateRejectsAMismatchingImage(t *testing.T) {
	images := map[string][]byte{ipxeImage: []byte("plain image"), ipxeSBImage: []byte("tampered")}
	published := map[string]string{ipxeImage: digestOf(images[ipxeImage]), ipxeSBImage: digestOf([]byte("signed image"))}
	fakeReleases(t, images, published, "3.0.3")
	before := netbootState{IPXE: "v1.21.1", Netbootxyz: "2.0.80"}
	st, err := updateNetbootReleases(context.Background(), before)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatal("tampered image accepted:", err)
	}
	if st.IPXE != before.IPXE || st.Netbootxyz != before.Netbootxyz {
		t.Fatalf("state changed on failure: %+v", st)
	}
	if _, err := os.Stat(ipxeImagePath("v2.0.0", ipxeSBImage)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a mismatching download took the image's place")
	}
	if _, err := os.Stat(netbootStatePath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("state saved on failure")
	}
}

func TestVerifyFileDetectsDamage(t *testing.T) {
	path := filepath.Join(t.TempDir(), ipxeImage)
	if err := os.WriteFile(path, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyFile(path, digestOf([]byte("image"))); err != nil {
		t.Fatal(err)
	}
	if err := verifyFile(path, digestOf([]byte("other"))); err == nil {
		t.Fatal("damaged image accepted")
	}
}

// fakeUSBNetwork gives the network boot code temporary boot and runtime
// directories and records how often the USB network is applied.

// Sharing the user turned on stays on.
