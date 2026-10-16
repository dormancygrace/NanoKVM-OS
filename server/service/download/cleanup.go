package download

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"NanoKVM-Server/service/storage"
	log "github.com/sirupsen/logrus"
)

// Only os.CreateTemp's exact transfer names are reserved. Never sweep all
// dotfiles or a user's .nanokvm-* directory; these transfers live in /data's
// root, not inside the user disk or other mounted images.
func imageTransferTemp(name string) bool {
	for _, prefix := range []string{".nanokvm-upload-", ".nanokvm-download-"} {
		digits, ok := strings.CutPrefix(name, prefix)
		if !ok || digits == "" || strings.Trim(digits, "0123456789") != "" {
			continue
		}
		if _, err := strconv.ParseUint(digits, 10, 32); err == nil {
			return true
		}
	}
	return false
}

func cleanupImageTransfers(dir, metadata string) {
	keep := ""
	if job, err := loadImageResume(metadata); err == nil {
		info, err := os.Lstat(job.Path)
		if err == nil && info.Mode().IsRegular() && info.Size() > 0 && info.Size() < job.Total {
			keep = job.Path
		}
	}
	root, err := os.Open(dir)
	if err != nil {
		return
	}
	defer root.Close()
	for {
		entries, readErr := root.ReadDir(128)
		for _, entry := range entries {
			if !imageTransferTemp(entry.Name()) {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			if path == keep {
				continue
			}
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			if err := storage.RemoveImage(path); err != nil {
				log.Warnf("Cannot remove interrupted image transfer %s: %v", entry.Name(), err)
			}
		}
		if errors.Is(readErr, io.EOF) {
			return
		}
		if readErr != nil {
			log.Warnf("Cannot scan interrupted image transfers: %v", readErr)
			return
		}
	}
}
