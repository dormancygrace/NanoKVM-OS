package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/gin-gonic/gin"

	"NanoKVM-Server/proto"
)

// MCP's virtual_media mounts through MountImageFile: the same refusals as the
// page while remote media or network boot own the virtual disk, before the
// disk is touched, and the page still gets 409 for them.
func TestMountImageFileRefusesWhileAnotherMediumOwnsTheDisk(t *testing.T) {
	originalUDC := usbGadgetUDC
	usbGadgetUDC = filepath.Join(t.TempDir(), "missing-udc")
	t.Cleanup(func() { usbGadgetUDC = originalUDC })

	for _, s := range []*Service{{remote: &remoteSession{}}, {netboot: &netbootSession{}}} {
		err := s.MountImageFile("", false)
		var mountErr *imageMountError
		if !errors.As(err, &mountErr) || !mountErr.conflict {
			t.Fatalf("mount with remote=%v netboot=%v: %v", s.remote != nil, s.netboot != nil, err)
		}
	}
	// Free: the mount goes on and meets the inactive USB storage.
	var mountErr *imageMountError
	if err := NewService().MountImageFile("", false); !errors.As(err, &mountErr) || mountErr.code != -3 {
		t.Fatalf("mount on a free disk: %v", err)
	}
	if err := NewService().MountImageFile("../etc/passwd", false); !errors.As(err, &mountErr) || mountErr.msg != "invalid image" {
		t.Fatalf("mount outside /data: %v", err)
	}

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(http.MethodPost, "/api/storage/image/mount", bytes.NewBufferString(`{"file":""}`))
	request.Header.Set("Content-Type", "application/json")
	context.Request = request
	(&Service{netboot: &netbootSession{}}).MountImage(context)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("page mount during network boot: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestImageFileAcceptsOnlyListedImages(t *testing.T) {
	root, outside := imageFixture(t)

	for _, name := range []string{"disk.iso", "sub/disk.IMG"} {
		path := filepath.Join(root, name)
		if got, err := imageFile(path); err != nil || got != path {
			t.Errorf("imageFile(%q) = %q, %v; want the file", path, got, err)
		}
	}
	for _, path := range []string{
		filepath.Join(root, "notes.txt"),
		filepath.Join(root, "link.img"),
		filepath.Join(root, "dir.iso"),
		filepath.Join(root, "missing.iso"),
		root + "/../" + filepath.Base(outside) + "/secret.img",
		filepath.Join(root, ".nanokvm", "user-disk.img"),
		"/dev/mmcblk0",
		"",
	} {
		if got, err := imageFile(path); err == nil {
			t.Errorf("imageFile(%q) = %q; want an error", path, got)
		}
	}
}

func TestGetImagesListsOnlyMountableImages(t *testing.T) {
	root, _ := imageFixture(t)
	if err := os.Symlink(filepath.Join(root, "sub"), filepath.Join(root, "linkdir.iso")); err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/storage/image", nil)
	NewService().GetImages(context)

	var rsp struct {
		Code int                `json:"code"`
		Data proto.GetImagesRsp `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &rsp); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
	want := []string{filepath.Join(root, "disk.iso"), filepath.Join(root, "sub", "disk.IMG")}
	if rsp.Code != 0 || !slices.Equal(rsp.Data.Files, want) {
		t.Fatalf("listed %v (code %d); want %v", rsp.Data.Files, rsp.Code, want)
	}
	for _, path := range rsp.Data.Files {
		if _, err := imageFile(path); err != nil {
			t.Errorf("listed %q cannot be mounted or deleted: %v", path, err)
		}
	}
}

// imageFixture points imageDirectory at a new directory with two images and
// entries that are not mountable images: a text file, a symbolic link to an
// image outside the directory, a directory with an image suffix, and the
// user disk in its hidden directory.
func imageFixture(t *testing.T) (root, outside string) {
	t.Helper()
	root = t.TempDir()
	original := imageDirectory
	imageDirectory = root
	t.Cleanup(func() { imageDirectory = original })
	outside = t.TempDir()
	for _, name := range []string{"disk.iso", "sub/disk.IMG", "notes.txt", ".nanokvm/user-disk.img"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.img"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.img"), filepath.Join(root, "link.img")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "dir.iso"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root, outside
}
