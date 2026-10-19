package common

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
)

// rgbRangeCapability is the Video Capability Data Block that
// scripts/edid-rgb-range.py appends to every shipped profile but the stock
// one: QS=1 lets the source send full-range RGB, which the receiver expects.
const rgbRangeCapability = 0x4A

// profileWithoutRGBRange undoes scripts/edid-rgb-range.py: it returns the
// profile as earlier releases shipped it, or nil when the profile carries no
// such block.
func profileWithoutRGBRange(edid []byte) []byte {
	if len(edid) != 256 || !edidValid(edid) || edid[126] != 1 || edid[128] != 2 {
		return nil
	}
	end := int(edid[130])
	if end < 4 || end > 127 {
		return nil
	}
	for pos := 4; pos < end; {
		header := edid[128+pos]
		length := int(header & 31)
		if pos+1+length > end {
			return nil
		}
		if header>>5 == 7 && length == 2 && edid[128+pos+1] == 0 && edid[128+pos+2] == rgbRangeCapability {
			old := append([]byte(nil), edid...)
			ext := old[128:]
			copy(ext[pos:127], ext[pos+3:127])
			ext[124], ext[125], ext[126] = 0, 0, 0
			ext[2] = byte(end - 3)
			var sum byte
			for _, b := range ext[:127] {
				sum += b
			}
			ext[127] = -sum
			return old
		}
		pos += 1 + length
	}
	return nil
}

// rgbRangeSuccessorLocked finds the shipped profile whose earlier version,
// without the RGB range block, has the recorded hash: the receiver then holds
// that profile as an earlier release programmed it. Same resolution and rate,
// so the current version replaces it without changing the monitor mode.
func rgbRangeSuccessorLocked(recorded string) string {
	if shippedMonitorProfileLocked(recorded) != "" {
		return "" // a current profile, such as the stock one
	}
	files, _ := filepath.Glob(filepath.Join(monitorEDIDDir, "NanoKVM-*.bin"))
	sort.Strings(files)
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if old := profileWithoutRGBRange(data); old != nil {
			if sum := sha256.Sum256(old); hex.EncodeToString(sum[:]) == recorded {
				return path
			}
		}
	}
	return ""
}
