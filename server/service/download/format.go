package download

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Raw disk images may contain partitioned disks or a filesystem directly.
// Do not limit .img to a particular partition table or filesystem signature.
func validateUploadedImage(path, name string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if strings.EqualFold(filepath.Ext(name), ".img") {
		st, err := f.Stat()
		if err != nil {
			return err
		}
		if st.Size() >= 512 && st.Size()%512 == 0 {
			return nil
		}
		return fmt.Errorf("raw disk image must contain complete 512-byte sectors")
	}
	if opticalImage(f) {
		return nil
	}
	return fmt.Errorf("file is not an ISO 9660 or UDF optical image")
}

// ISO/UDF volume recognition sequences start at sector 16. Pure UDF has no
// CD001 descriptor; NSR02/NSR03, following BEA01, identifies it.
func opticalImage(r io.ReaderAt) bool {
	var id [5]byte
	udf := false
	for i := 0; i < 32; i++ {
		if _, err := r.ReadAt(id[:], int64(0x8001+i*2048)); err != nil {
			return false
		}
		switch string(id[:]) {
		case "CD001":
			return true
		case "BEA01":
			udf = true
		case "NSR02", "NSR03":
			return udf
		case "BOOT2", "CDW02":
		default:
			return false
		}
	}
	return false
}
