package download

import "testing"

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
