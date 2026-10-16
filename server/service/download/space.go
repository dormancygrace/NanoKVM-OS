package download

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"

	"golang.org/x/sys/unix"
)

const imageFreeReserve = uint64(128 << 20)

var errImageSpace = errors.New("not enough free space for the image (128 MiB reserved)")
var imageWriteBudget = fileImageBudget

// A single image transfer runs at a time. The budget is for additional bytes,
// not the full size of an already partially downloaded image.
func fileImageBudget(file *os.File) (int64, error) {
	var stat unix.Statfs_t
	if err := unix.Fstatfs(int(file.Fd()), &stat); err != nil {
		return 0, fmt.Errorf("read free space: %w", err)
	}
	available := stat.Bavail * uint64(stat.Bsize)
	if available <= imageFreeReserve {
		return 0, errImageSpace
	}
	return int64(min(available-imageFreeReserve, uint64(math.MaxInt64-1))), nil
}

type imageBudgetWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *imageBudgetWriter) Write(p []byte) (int, error) {
	over := int64(len(p)) > w.remaining
	if over {
		p = p[:w.remaining]
	}
	n, err := w.writer.Write(p)
	w.remaining -= int64(n)
	if err == nil && over {
		err = errImageSpace
	}
	return n, err
}

func copyImageWithin(dst io.Writer, src io.Reader, expected []byte, budget int64) (int64, error) {
	return copyImage(&imageBudgetWriter{writer: dst, remaining: budget}, src, expected)
}
