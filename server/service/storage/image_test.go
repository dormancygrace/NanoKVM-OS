package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"NanoKVM-Server/proto"
)

func TestRequireActiveUSBStorage(t *testing.T) {
	dir := t.TempDir()
	original := []string{usbGadgetUDC, usbStorageLink, mountDevice, forcedEjectDevice, roFlag, cdromFlag, inquiryString}
	t.Cleanup(func() {
		usbGadgetUDC, usbStorageLink, mountDevice, forcedEjectDevice, roFlag, cdromFlag, inquiryString =
			original[0], original[1], original[2], original[3], original[4], original[5], original[6]
	})

	usbGadgetUDC = filepath.Join(dir, "UDC")
	usbStorageLink = filepath.Join(dir, "configs", "c.1", "mass_storage.disk0")
	attrs := filepath.Join(dir, "functions", "mass_storage.disk0", "lun.0")
	mountDevice = filepath.Join(attrs, "file")
	forcedEjectDevice = filepath.Join(attrs, "forced_eject")
	roFlag = filepath.Join(attrs, "ro")
	cdromFlag = filepath.Join(attrs, "cdrom")
	inquiryString = filepath.Join(attrs, "inquiry_string")
	if err := os.MkdirAll(filepath.Dir(usbStorageLink), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(attrs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(usbGadgetUDC, []byte("dummy_udc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{mountDevice, forcedEjectDevice, roFlag, cdromFlag, inquiryString} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join("..", "..", "functions", "mass_storage.disk0"), usbStorageLink); err != nil {
		t.Fatal(err)
	}

	if err := requireActiveUSBStorage(); err != nil {
		t.Fatalf("active USB storage rejected: %v", err)
	}
	if err := os.WriteFile(usbGadgetUDC, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := requireActiveUSBStorage(); err == nil {
		t.Fatal("unbound USB gadget accepted")
	}
}

func TestRequireActiveUSBStorageRejectsUnlinkedFunction(t *testing.T) {
	dir := t.TempDir()
	originalUDC, originalLink := usbGadgetUDC, usbStorageLink
	usbGadgetUDC, usbStorageLink = filepath.Join(dir, "UDC"), filepath.Join(dir, "mass_storage.disk0")
	t.Cleanup(func() { usbGadgetUDC, usbStorageLink = originalUDC, originalLink })
	if err := os.WriteFile(usbGadgetUDC, []byte("dummy_udc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(usbStorageLink, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := requireActiveUSBStorage(); err == nil {
		t.Fatal("regular file accepted as a linked USB storage function")
	}
}

func TestEjectLocalImageUsesForcedEjectOnlyWhenRequested(t *testing.T) {
	originalMount, originalForce := mountDevice, forcedEjectDevice
	mountDevice, forcedEjectDevice = "mount", "force"
	t.Cleanup(func() { mountDevice, forcedEjectDevice = originalMount, originalForce })

	busy := errors.New("busy")
	var writes []string
	write := func(path string, _ []byte, _ os.FileMode) error {
		writes = append(writes, path)
		if path == mountDevice {
			return busy
		}
		return nil
	}
	if err := ejectLocalImage(false, write); !errors.Is(err, busy) {
		t.Fatalf("normal eject error = %v, want busy", err)
	}
	if len(writes) != 1 {
		t.Fatalf("normal eject unexpectedly forced: %v", writes)
	}
	writes = nil
	if err := ejectLocalImage(true, write); err != nil {
		t.Fatalf("forced eject: %v", err)
	}
	if len(writes) != 2 || writes[1] != forcedEjectDevice {
		t.Fatalf("forced eject writes = %v", writes)
	}
}

func TestMountImageRejectsForceWhileMounting(t *testing.T) {
	rsp := postMountImage(t, `{"file":"/data/test.iso","cdrom":true,"force":true}`)
	if rsp.Code != -1 || rsp.Msg != "force is only valid when ejecting an image" {
		t.Fatalf("response = %#v", rsp)
	}
}

func TestMountImageRejectsInactiveUSBStorage(t *testing.T) {
	originalUDC := usbGadgetUDC
	usbGadgetUDC = filepath.Join(t.TempDir(), "missing-udc")
	t.Cleanup(func() { usbGadgetUDC = originalUDC })

	rsp := postMountImage(t, `{"file":"","cdrom":true}`)
	if rsp.Code != -3 || rsp.Msg != "Enable USB storage before mounting an image" {
		t.Fatalf("response = %#v", rsp)
	}
}

func postMountImage(t *testing.T, body string) proto.Response {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(http.MethodPost, "/api/storage/image/mount", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	context.Request = request
	NewService().MountImage(context)

	var rsp proto.Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &rsp); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
	return rsp
}
