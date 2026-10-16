package storage

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"NanoKVM-Server/service/hid"
	"github.com/gin-gonic/gin"
)

func guardFixture(t *testing.T) (image, lun, marker string) {
	t.Helper()
	dir := t.TempDir()
	oldMount, oldMarkers, oldDirectory := mountDevice, imageBootMarkers, imageDirectory
	t.Cleanup(func() { mountDevice, imageBootMarkers, imageDirectory = oldMount, oldMarkers, oldDirectory })
	imageDirectory = filepath.Join(dir, "data")
	lun = filepath.Join(dir, "mass_storage.disk0/lun.0/file")
	marker = filepath.Join(dir, "usb.disk0")
	if err := os.MkdirAll(filepath.Dir(lun), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(imageDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	mountDevice, imageBootMarkers = lun, []string{marker}
	image = filepath.Join(imageDirectory, "setup.iso")
	writeGuardFile(t, image, "old image")
	writeGuardFile(t, lun, "")
	return
}
func writeGuardFile(t *testing.T, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestImageGuardRejectsAttachedAliasesAndUnknownState(t *testing.T) {
	for _, kind := range []string{"lun", "case", "hardlink", "marker", "other-lun", "unknown-lun", "unknown-marker"} {
		t.Run(kind, func(t *testing.T) {
			image, lun, marker := guardFixture(t)
			switch kind {
			case "lun":
				writeGuardFile(t, lun, image)
			case "case":
				writeGuardFile(t, lun, strings.ToUpper(image))
			case "hardlink":
				alias := filepath.Join(filepath.Dir(image), "alias.iso")
				if err := os.Link(image, alias); err != nil {
					t.Fatal(err)
				}
				writeGuardFile(t, lun, alias)
			case "marker":
				writeGuardFile(t, marker, image)
			case "other-lun":
				other := filepath.Join(filepath.Dir(filepath.Dir(lun)), "lun.2", "file")
				if err := os.MkdirAll(filepath.Dir(other), 0700); err != nil {
					t.Fatal(err)
				}
				writeGuardFile(t, other, image)
			case "unknown-lun":
				if err := os.Remove(lun); err != nil {
					t.Fatal(err)
				}
			case "unknown-marker":
				if err := os.Mkdir(marker, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := RemoveImage(image); err == nil {
				t.Fatal("removed protected image")
			}
			temp := filepath.Join(filepath.Dir(image), ".upload")
			writeGuardFile(t, temp, "new image")
			if err := InstallImage(temp, image); err == nil {
				t.Fatal("replaced protected image")
			}
			if data, _ := os.ReadFile(image); string(data) != "old image" {
				t.Fatal("image changed")
			}
			if _, err := os.Stat(temp); err != nil {
				t.Fatal("failed install consumed source")
			}
		})
	}
}

func TestDeleteImageHandlerRejectsAttachedImage(t *testing.T) {
	image, lun, _ := guardFixture(t)
	writeGuardFile(t, lun, image)
	body, _ := json.Marshal(map[string]string{"file": image})
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	NewService().DeleteImage(c)
	var rsp struct{ Code int }
	if err := json.Unmarshal(rec.Body.Bytes(), &rsp); err != nil {
		t.Fatal(err)
	}
	if rsp.Code == 0 {
		t.Fatalf("attached image deletion accepted: %s", rec.Body)
	}
	if _, err := os.Stat(image); err != nil {
		t.Fatal("handler deleted image")
	}
}

func TestImageMutationWaitsForCompositionAndRechecks(t *testing.T) {
	image, lun, _ := guardFixture(t)
	h := hid.GetHid()
	h.Lock()
	done := make(chan error, 1)
	go func() { done <- RemoveImage(image) }()
	select {
	case err := <-done:
		h.Unlock()
		t.Fatalf("mutation escaped composition lock: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	writeGuardFile(t, lun, image)
	h.Unlock()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("did not recheck newly attached image")
		}
	case <-time.After(time.Second):
		t.Fatal("mutation blocked after composition")
	}
}

func TestImageGuardAllowsIdleReplacementAndDeletion(t *testing.T) {
	image, lun, _ := guardFixture(t)
	writeGuardFile(t, lun, "/dev/nbd0")
	temp := filepath.Join(filepath.Dir(image), ".upload")
	writeGuardFile(t, temp, "new image")
	if err := InstallImage(temp, image); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(image); string(data) != "new image" {
		t.Fatal("image not replaced")
	}
	if err := RemoveImage(image); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(image); !os.IsNotExist(err) {
		t.Fatal("image not removed")
	}
}
