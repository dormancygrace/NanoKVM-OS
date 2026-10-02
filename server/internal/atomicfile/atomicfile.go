// Package atomicfile replaces files so that a reader, or the next boot after
// a power cut, sees either the complete old content or the complete new one.
package atomicfile

import (
	"os"
	"path/filepath"
)

// Write stores data at path via a synced temporary file in the same
// directory, an atomic rename and a sync of the directory entry.
func Write(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(perm); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	return SyncDir(dir)
}

// SyncDir makes a rename or file creation in dir durable.
func SyncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
