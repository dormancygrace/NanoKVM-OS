package utils

import (
	"errors"
	"path/filepath"
	"strings"
)

// ErrOutsideDirectory reports a name that does not stay inside its directory.
var ErrOutsideDirectory = errors.New("path is outside its directory")

// JoinWithin joins name below dir and rejects any result that is not strictly
// inside dir, such as an empty name or one that climbs out with "..".
func JoinWithin(dir, name string) (string, error) {
	dir = filepath.Clean(dir)
	path := filepath.Clean(filepath.Join(dir, name))
	if !strings.HasPrefix(path, dir+string(filepath.Separator)) {
		return "", ErrOutsideDirectory
	}
	return path, nil
}
