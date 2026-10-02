package download

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoteImageFilename(t *testing.T) {
	for _, name := range []string{"debian-13.iso", "disk.IMG", "rescue_1.img"} {
		if err := validateRemoteImageFilename(name); err != nil {
			t.Errorf("%q rejected: %v", name, err)
		}
	}
	for _, name := range []string{"", "..", "..iso", ".jwt_secret", "server.key", "a b.iso", "notes.txt", "x.iso/"} {
		if err := validateRemoteImageFilename(name); err == nil {
			t.Errorf("%q accepted", name)
		}
	}
	if err := validateISOFilename("disk.img"); err == nil || err.Error() != "only .iso files allowed" {
		t.Errorf("upload rule changed: %v", err)
	}
}

func TestResumeFollowsImageFilenamePolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume.json")
	job := &imageResume{URL: "https://example.test/a", Path: "/data/.nanokvm-download-123", ETag: `"abc"`, Total: 100}
	for _, name := range []string{"debian-13.iso", "disk.IMG"} {
		job.Filename = name
		if err := saveImageResume(path, job); err != nil {
			t.Fatal(err)
		}
		if got, err := loadImageResume(path); err != nil || got.Filename != name {
			t.Errorf("saved job for %q = %+v, %v; want it resumed", name, got, err)
		}
	}
	for _, name := range []string{"", "..", ".jwt_secret", "server.key", "notes.txt", "a b.iso", "../a.iso"} {
		job.Filename = name
		if err := saveImageResume(path, job); err != nil {
			t.Fatal(err)
		}
		if _, err := loadImageResume(path); err == nil {
			t.Errorf("saved job for %q was accepted", name)
		}
	}
}

func TestInstallImageAcceptsOnlyImageFilenames(t *testing.T) {
	dir := t.TempDir()
	partial := func() string {
		f, err := os.CreateTemp(dir, ".nanokvm-download-*")
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
		return f.Name()
	}
	for _, name := range []string{"debian-13.iso", "disk.IMG"} {
		if err := installImage(dir, partial(), name); err != nil {
			t.Errorf("installImage(%q): %v", name, err)
		}
		if info, err := os.Stat(filepath.Join(dir, name)); err != nil || !info.Mode().IsRegular() {
			t.Errorf("%q was not installed: %v", name, err)
		}
	}
	for _, name := range []string{".jwt_secret", "notes.txt", "../escape.iso"} {
		temp := partial()
		if err := installImage(dir, temp, name); err == nil {
			t.Errorf("installImage(%q) succeeded", name)
		}
		if _, err := os.Stat(temp); err != nil {
			t.Errorf("rejected %q moved the partial file: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.iso")); err == nil {
		t.Error("an image was installed outside its directory")
	}
}
