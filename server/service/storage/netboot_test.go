package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"NanoKVM-Server/usbinternet"
)

func TestNetbootScriptLoadsTheReleaseMenus(t *testing.T) {
	script := netbootScript("3.0.3")
	for _, want := range []string{
		"#!ipxe\n",
		"set boot_domain boot.netboot.xyz/3.0.3\n",
		"set version 3.0.3\n",
		"chain --autofree https://${boot_domain}/menu.ipxe || chain --autofree http://${boot_domain}/menu.ipxe || goto failed\n",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script lacks %q:\n%s", want, script)
		}
	}
	if !strings.HasPrefix(script, "#!ipxe\n") {
		t.Fatal("not an iPXE script")
	}
}

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

func TestIPXEDigests(t *testing.T) {
	good := githubRelease{Tag: "v2.0.0", Assets: []githubAsset{
		{Name: ipxeImage, Size: 10, Digest: "sha256:" + strings.Repeat("a", 64)},
		{Name: ipxeSBImage, Size: 10, Digest: "sha256:" + strings.Repeat("b", 64)},
		{Name: "ipxe.usb", Size: 10},
	}}
	got, err := ipxeDigests(good)
	if err != nil || got[ipxeImage] != strings.Repeat("a", 64) || got[ipxeSBImage] != strings.Repeat("b", 64) || len(got) != 2 {
		t.Fatal(got, err)
	}
	noDigest := good
	noDigest.Assets = []githubAsset{good.Assets[0], {Name: ipxeSBImage, Size: 10}}
	if _, err := ipxeDigests(noDigest); err == nil {
		t.Fatal("asset without a digest accepted")
	}
	missing := good
	missing.Assets = good.Assets[:1]
	if _, err := ipxeDigests(missing); err == nil {
		t.Fatal("release without the Secure Boot image accepted")
	}
	huge := good
	huge.Assets = []githubAsset{good.Assets[0], {Name: ipxeSBImage, Size: ipxeMaxBytes + 1, Digest: good.Assets[1].Digest}}
	if _, err := ipxeDigests(huge); err == nil {
		t.Fatal("oversized image accepted")
	}
	badTag := good
	badTag.Tag = "latest"
	if _, err := ipxeDigests(badTag); err == nil {
		t.Fatal("unexpected tag accepted")
	}
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

func TestUpdateInstallsVerifiedImages(t *testing.T) {
	images := map[string][]byte{ipxeImage: []byte("plain image"), ipxeSBImage: []byte("signed image")}
	published := map[string]string{ipxeImage: digestOf(images[ipxeImage]), ipxeSBImage: digestOf(images[ipxeSBImage])}
	fakeReleases(t, images, published, "3.0.3")
	stale := filepath.Join(netbootDir, "ipxe-v1.21.1")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}

	st, err := updateNetbootReleases(context.Background(), netbootState{SecureBoot: true})
	if err != nil {
		t.Fatal(err)
	}
	if st.IPXE != "v2.0.0" || st.Netbootxyz != "3.0.3" || !st.SecureBoot || st.CheckedAt == "" {
		t.Fatalf("%+v", st)
	}
	for name, data := range images {
		got, err := os.ReadFile(ipxeImagePath("v2.0.0", name))
		if err != nil || string(got) != string(data) {
			t.Fatal(name, err)
		}
	}
	saved, err := loadNetbootState()
	if err != nil || saved.IPXE != "v2.0.0" || saved.IPXEFiles[ipxeSBImage] != published[ipxeSBImage] {
		t.Fatal(saved, err)
	}
	removeOldIPXE(st.IPXE)
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the old release was kept")
	}
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

func TestUpdateRejectsAnUnservedNetbootxyzRelease(t *testing.T) {
	images := map[string][]byte{ipxeImage: []byte("a"), ipxeSBImage: []byte("b")}
	published := map[string]string{ipxeImage: digestOf([]byte("a")), ipxeSBImage: digestOf([]byte("b"))}
	fakeReleases(t, images, published, "3.0.3")
	netbootxyzMenuBase += "/elsewhere"
	if _, err := updateNetbootReleases(context.Background(), netbootState{}); err == nil {
		t.Fatal("a release whose menus are not served was selected")
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

func TestMenuListenerServesOnlyTheScript(t *testing.T) {
	n := &netbootSession{stop: make(chan struct{})}
	n.netbootxyz.Store("3.0.3")
	server := httptest.NewServer(n.handler())
	defer server.Close()

	rsp, err := http.Get(server.URL + "/boot.ipxe")
	if err != nil {
		t.Fatal(err)
	}
	body := make([]byte, 4096)
	k, _ := rsp.Body.Read(body)
	rsp.Body.Close()
	if rsp.StatusCode != 200 || !strings.Contains(string(body[:k]), "boot.netboot.xyz/3.0.3") {
		t.Fatal(rsp.StatusCode, string(body[:k]))
	}
	if n.fetches.Load() != 1 || n.lastFetch.Load() == 0 {
		t.Fatal("fetch not counted")
	}
	for _, path := range []string{"/", "/data/", "/boot.ipxe/../state.json", "/menu.ipxe"} {
		rsp, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		rsp.Body.Close()
		if rsp.StatusCode != 404 {
			t.Fatalf("%s answered %d", path, rsp.StatusCode)
		}
	}
	rsp, err = http.Post(server.URL+"/boot.ipxe", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	rsp.Body.Close()
	if rsp.StatusCode != 405 {
		t.Fatal("POST answered", rsp.StatusCode)
	}
}

// fakeUSBNetwork gives the network boot code temporary boot and runtime
// directories and records how often the USB network is applied.
func fakeUSBNetwork(t *testing.T) (usbinternet.Runtime, *int) {
	t.Helper()
	r := usbinternet.Runtime{Boot: t.TempDir(), Run: t.TempDir()}
	applied := 0
	oldRuntime, oldApply, oldDir := netbootRuntime, applyUSBNetwork, netbootDir
	t.Cleanup(func() { netbootRuntime, applyUSBNetwork, netbootDir = oldRuntime, oldApply, oldDir })
	netbootRuntime = func() usbinternet.Runtime { return r }
	applyUSBNetwork = func() error { applied++; return nil }
	netbootDir = t.TempDir()
	return r, &applied
}

func TestUndoRestoresTheUsersSharingSetting(t *testing.T) {
	r, applied := fakeUSBNetwork(t)
	if err := setSharing(r, true); err != nil {
		t.Fatal(err)
	}
	if err := r.SetNetbootPort(netbootPort); err != nil {
		t.Fatal(err)
	}
	st := netbootState{IPXE: "v2.0.0", SharingByNetboot: true}
	if err := undoNetboot(&st); err != nil {
		t.Fatal(err)
	}
	if r.Enabled() || r.NetbootPort() != 0 || st.SharingByNetboot || *applied != 1 {
		t.Fatalf("sharing %v port %d state %+v applied %d", r.Enabled(), r.NetbootPort(), st, *applied)
	}
	if saved, err := loadNetbootState(); err != nil || saved.SharingByNetboot || saved.IPXE != "v2.0.0" {
		t.Fatal(saved, err)
	}

	// Sharing the user turned on stays on.
	if err := setSharing(r, true); err != nil {
		t.Fatal(err)
	}
	st = netbootState{}
	if err := undoNetboot(&st); err != nil || !r.Enabled() {
		t.Fatal("the user's sharing was turned off:", err)
	}
}

func TestEnableNeedsUSBNetworkAndDisk(t *testing.T) {
	fakeUSBNetwork(t)
	oldLink := usbNetworkLink
	t.Cleanup(func() { usbNetworkLink = oldLink })
	usbNetworkLink = filepath.Join(t.TempDir(), "ncm.usb0")
	s := NewService()
	code, err := s.enableNetboot(context.Background(), true)
	if code != -6 || err == nil || !strings.Contains(err.Error(), "USB network") {
		t.Fatal(code, err)
	}
	if err := os.Symlink("../functions/ncm.usb0", usbNetworkLink); err != nil {
		t.Fatal(err)
	}
	// The test host has no gadget, so the virtual disk check fails next.
	code, err = s.enableNetboot(context.Background(), true)
	if code != -6 || err == nil || !strings.Contains(err.Error(), "virtual disk") {
		t.Fatal(code, err)
	}
	if s.netboot != nil {
		t.Fatal("session started")
	}
}

func TestEnableIsRefusedWithBrowserMedia(t *testing.T) {
	fakeUSBNetwork(t)
	s := NewService()
	s.remote = &remoteSession{}
	if code, err := s.enableNetboot(context.Background(), false); code != -5 || err == nil {
		t.Fatal(code, err)
	}
}

func TestMountingIsRefusedDuringNetworkBoot(t *testing.T) {
	s := NewService()
	s.netboot = &netbootSession{stop: make(chan struct{})}
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/storage/image/mount", strings.NewReader(`{"file":"/data/a.iso","cdrom":true}`))
	c.Request.Header.Set("Content-Type", "application/json")
	s.MountImage(c)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "network boot") {
		t.Fatal(rec.Code, rec.Body.String())
	}
}

func TestCleanupAfterRestartUndoesWhatWasLeftOn(t *testing.T) {
	r, applied := fakeUSBNetwork(t)
	oldMount := mountNetbootImage
	t.Cleanup(func() { mountNetbootImage = oldMount })
	mountNetbootImage = func(string, bool, bool) error { return fmt.Errorf("no gadget in tests") }
	if err := setSharing(r, true); err != nil {
		t.Fatal(err)
	}
	if err := r.SetNetbootPort(netbootPort); err != nil {
		t.Fatal(err)
	}
	if err := saveNetbootState(netbootState{SharingByNetboot: true}); err != nil {
		t.Fatal(err)
	}
	NewService().CleanupNetboot()
	if r.Enabled() || r.NetbootPort() != 0 || *applied != 1 {
		t.Fatalf("sharing %v port %d applied %d", r.Enabled(), r.NetbootPort(), *applied)
	}

	// Nothing to undo: nothing is applied.
	NewService().CleanupNetboot()
	if *applied != 1 {
		t.Fatal("applied without a reason")
	}
}

func fakeNetbootLUN(t *testing.T) {
	t.Helper()
	oldMount, oldForce := mountDevice, forcedEjectDevice
	t.Cleanup(func() { mountDevice, forcedEjectDevice = oldMount, oldForce })
	dir := t.TempDir()
	mountDevice, forcedEjectDevice = filepath.Join(dir, "file"), filepath.Join(dir, "forced_eject")
	if err := os.WriteFile(mountDevice, []byte(ipxeImagePath("v2.0.0", ipxeImage)), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestNetbootDisableRetainsSessionOnEjectError(t *testing.T) {
	r, _ := fakeUSBNetwork(t)
	fakeNetbootLUN(t)
	if err := setSharing(r, true); err != nil {
		t.Fatal(err)
	}
	if err := r.SetNetbootPort(netbootPort); err != nil {
		t.Fatal(err)
	}
	if err := saveNetbootState(netbootState{SharingByNetboot: true}); err != nil {
		t.Fatal(err)
	}
	n := &netbootSession{image: ipxeImagePath("v2.0.0", ipxeImage), stop: make(chan struct{})}
	s := NewService()
	s.netboot = n
	// Make the LUN unreadable even when tests run as root.
	if err := os.Remove(mountDevice); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(mountDevice, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.disableNetboot(); err == nil {
		t.Fatal("eject failure hidden")
	}
	if s.netboot != n || !r.Enabled() || r.NetbootPort() != netbootPort {
		t.Fatal("failed disable discarded session or cleanup markers")
	}
	select {
	case <-n.stop:
		t.Fatal("listener stopped before successful cleanup")
	default:
	}
	if err := os.Remove(mountDevice); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mountDevice, []byte(n.image), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.disableNetboot(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(mountDevice)
	if err != nil || strings.TrimSpace(string(data)) != "" || s.netboot != nil || r.Enabled() || r.NetbootPort() != 0 {
		t.Fatal("retry did not finish cleanup", string(data), err)
	}
	select {
	case <-n.stop:
	default:
		t.Fatal("listener still active")
	}
}

func TestNetbootDisableRetriesNetworkCleanup(t *testing.T) {
	r, _ := fakeUSBNetwork(t)
	fakeNetbootLUN(t)
	if err := saveNetbootState(netbootState{SharingByNetboot: true}); err != nil {
		t.Fatal(err)
	}
	n := &netbootSession{stop: make(chan struct{})}
	s := NewService()
	s.netboot = n
	applyUSBNetwork = func() error { return errors.New("apply failed") }
	if _, err := s.disableNetboot(); err == nil {
		t.Fatal("cleanup failure hidden")
	}
	st, err := loadNetbootState()
	if err != nil || !st.SharingByNetboot || s.netboot != n {
		t.Fatal("retry state lost", st, err)
	}
	applyUSBNetwork = func() error { return nil }
	if _, err := s.disableNetboot(); err != nil {
		t.Fatal(err)
	}
	if s.netboot != nil || r.NetbootPort() != 0 {
		t.Fatal("retry did not finish")
	}
}

func TestCleanupEjectsOrphanWithoutRuntimeMarkers(t *testing.T) {
	fakeUSBNetwork(t)
	fakeNetbootLUN(t)
	s := NewService()
	s.CleanupNetboot()
	data, err := os.ReadFile(mountDevice)
	if err != nil || strings.TrimSpace(string(data)) != "" {
		t.Fatal("orphaned image retained", string(data), err)
	}
	unrelated := "/data/user.iso"
	if err := os.WriteFile(mountDevice, []byte(unrelated), 0600); err != nil {
		t.Fatal(err)
	}
	s.CleanupNetboot()
	data, err = os.ReadFile(mountDevice)
	if err != nil || string(data) != unrelated {
		t.Fatal("unrelated image ejected", err)
	}
}

func TestEnableEjectsPartiallyMountedImage(t *testing.T) {
	r, _ := fakeUSBNetwork(t)
	fakeNetbootLUN(t)
	originals := []string{usbGadgetUDC, usbStorageLink, roFlag, cdromFlag, inquiryString, usbNetworkLink, usbInternetTool}
	oldMount := mountNetbootImage
	t.Cleanup(func() {
		usbGadgetUDC, usbStorageLink, roFlag, cdromFlag, inquiryString, usbNetworkLink, usbInternetTool = originals[0], originals[1], originals[2], originals[3], originals[4], originals[5], originals[6]
		mountNetbootImage = oldMount
	})
	r.Gadget = t.TempDir()
	r.Net = t.TempDir()
	netbootRuntime = func() usbinternet.Runtime { return r }
	if err := os.MkdirAll(filepath.Join(r.Gadget, "configs/c.1"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(r.Net, "usb0"), 0700); err != nil {
		t.Fatal(err)
	}
	usbGadgetUDC = filepath.Join(r.Gadget, "UDC")
	usbStorageLink = filepath.Join(r.Gadget, "configs/c.1/mass_storage.disk0")
	usbNetworkLink = filepath.Join(r.Gadget, "configs/c.1/ncm.usb0")
	for _, p := range []string{usbStorageLink, usbNetworkLink} {
		if err := os.Symlink("unused", p); err != nil {
			t.Fatal(err)
		}
	}
	roFlag, cdromFlag, inquiryString, usbInternetTool = filepath.Join(r.Gadget, "ro"), filepath.Join(r.Gadget, "cdrom"), filepath.Join(r.Gadget, "inquiry"), filepath.Join(r.Gadget, "tool")
	for _, p := range []string{usbGadgetUDC, roFlag, cdromFlag, inquiryString, usbInternetTool, forcedEjectDevice} {
		if err := os.WriteFile(p, []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	status, _ := json.Marshal(usbinternet.Status{Address: "127.0.0.1/8", UpdatedAt: time.Now().Unix()})
	if err := os.WriteFile(filepath.Join(r.Run, "status.json"), status, 0600); err != nil {
		t.Fatal(err)
	}
	image := ipxeImagePath("v2.0.0", ipxeImage)
	if err := writeFileAtomic(image, []byte("image")); err != nil {
		t.Fatal(err)
	}
	if err := saveNetbootState(netbootState{IPXE: "v2.0.0", Netbootxyz: "3.0.3", IPXEFiles: map[string]string{ipxeImage: digestOf([]byte("image"))}}); err != nil {
		t.Fatal(err)
	}
	mountNetbootImage = func(path string, _, _ bool) error {
		if err := os.WriteFile(mountDevice, []byte(path), 0600); err != nil {
			t.Fatal(err)
		}
		return errors.New("USB rebind failed after mounting")
	}
	s := NewService()
	if _, err := s.enableNetboot(context.Background(), false); err == nil || !strings.Contains(err.Error(), "USB rebind failed") {
		t.Fatal("mount failure not reached", err)
	}
	data, err := os.ReadFile(mountDevice)
	if err != nil || strings.TrimSpace(string(data)) != "" || s.netboot != nil || r.Enabled() || r.NetbootPort() != 0 {
		t.Fatal("failed enable left state behind", string(data), err)
	}
}
