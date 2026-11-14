package helperdisk

import (
	"bytes"
	"os"
	"path/filepath"

	"NanoKVM-Server/internal/atomicfile"
)

// Publish puts the image at path unless it is there already (after a restart
// the disk is usually the same: the card is not written), and reports
// whether it wrote. A failed write is an error also when an older image
// could be read: the caller must not take the new one as published.
func Publish(path string, image []byte) (written bool, err error) {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, image) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := atomicfile.Write(path, image, 0o644); err != nil {
		return false, err
	}
	return true, nil
}
