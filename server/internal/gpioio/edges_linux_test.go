//go:build linux

package gpioio

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestEdgeRequestIsInputWithBothEdges(t *testing.T) {
	r := edgeRequest(25)
	if r.Num_lines != 1 || r.Offsets[0] != 25 || r.Config.Num_attrs != 0 {
		t.Fatal(r)
	}
	if r.Config.Flags != flagInput|flagEdgeRising|flagEdgeFalling {
		t.Fatalf("flags = %#x", r.Config.Flags)
	}
}

func TestUnsupportedEdgesClassifiesErrnos(t *testing.T) {
	for _, errno := range []error{unix.EINVAL, unix.ENXIO, unix.EOPNOTSUPP, errnoENOTSUPP} {
		err := unsupportedEdges(fmt.Errorf("request gpio-v2:chip:1: %w", errno))
		if !errors.Is(err, ErrEdgesUnsupported) || !errors.Is(err, errno) {
			t.Fatalf("%v not marked unsupported: %v", errno, err)
		}
	}
	for _, errno := range []error{unix.EBUSY, unix.EACCES, os.ErrClosed} {
		if err := unsupportedEdges(errno); errors.Is(err, ErrEdgesUnsupported) {
			t.Fatalf("%v marked unsupported", errno)
		}
	}
	if _, err := OpenEdges("/sys/class/gpio/gpio504/value"); !errors.Is(err, ErrEdgesUnsupported) {
		t.Fatalf("sysfs line: %v", err)
	}
}

func TestEdgeLineRequiresPollableFD(t *testing.T) {
	fd, err := unix.Open(filepath.Join(t.TempDir(), "line"), unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newEdgeLine(fd); !errors.Is(err, ErrEdgesUnsupported) {
		t.Fatalf("regular file accepted: %v", err)
	}
}
