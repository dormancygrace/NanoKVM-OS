package common

import "fmt"

// cubeMaxPixelRate is the active pixel rate of 1920x1080 at 60 Hz with 1 Hz
// of tolerance for rounded timings.
const cubeMaxPixelRate = 1920 * 1080 * 61

// cubeUnsafeVICs are the CTA-861 video codes wider or taller than 1920x1080,
// or faster than 1920x1080 at 60 Hz: 1080p100/120, 1680x720p120, the
// 2560x1080, 3840x2160, 4096x2160 and 5120x2160 modes, and 7680/10240 modes.
var cubeUnsafeVICs = func() map[byte]bool {
	vics := map[byte]bool{63: true, 64: true, 77: true, 78: true, 85: true, 113: true}
	for _, r := range [][2]int{{86, 107}, {114, 127}, {193, 219}} {
		for v := r[0]; v <= r[1]; v++ {
			vics[byte(v)] = true
		}
	}
	return vics
}()

// edidValid checks the EDID header and the checksum of every block present,
// so a failed read (all zeros or all ones) is not taken for an EDID without
// fast modes.
func edidValid(edid []byte) bool {
	if len(edid) < 128 || len(edid)%128 != 0 || string(edid[:8]) != "\x00\xff\xff\xff\xff\xff\xff\x00" {
		return false
	}
	// Never classify a partial read as safe: an omitted extension may contain
	// the timing that requires replacing the receiver profile.
	blocks := int(edid[126]) + 1
	if blocks*128 > len(edid) {
		return false
	}
	for block := 0; block < blocks; block++ {
		var sum byte
		for _, b := range edid[block*128 : (block+1)*128] {
			sum += b
		}
		if sum != 0 {
			return false
		}
	}
	return true
}

// edidBeyondCube reports the first mode an EDID advertises that a Cube
// receiver cannot capture: a timing wider than 1920 or taller than 1080
// pixels, or with more pixels per second than 1920x1080 at 60 Hz. It checks
// the detailed and standard timings of the base block and, in CTA-861
// extensions, the detailed timings, the short video descriptors, the 4K
// HDMI VICs and the YCbCr 4:2:0 video data block.
func edidBeyondCube(edid []byte) (string, bool) {
	if len(edid) < 128 {
		return "", false
	}
	tooFast := func(h, v int, hz float64) bool {
		return h > 1920 || v > 1080 || float64(h*v)*hz > cubeMaxPixelRate
	}
	detailed := func(d []byte) (string, bool) {
		clock := int(d[0]) | int(d[1])<<8 // 10 kHz units; 0 marks a descriptor
		if clock == 0 {
			return "", false
		}
		h := int(d[2]) | int(d[4]&0xf0)<<4
		hb := int(d[3]) | int(d[4]&0x0f)<<8
		v := int(d[5]) | int(d[7]&0xf0)<<4
		vb := int(d[6]) | int(d[7]&0x0f)<<8
		if h+hb == 0 || v+vb == 0 {
			return "", false
		}
		hz := float64(clock) * 10000 / float64((h+hb)*(v+vb))
		return fmt.Sprintf("%dx%d at %.0f Hz", h, v, hz), tooFast(h, v, hz)
	}
	for _, offset := range []int{54, 72, 90, 108} {
		if mode, bad := detailed(edid[offset : offset+18]); bad {
			return mode, true
		}
	}
	for offset := 38; offset < 54; offset += 2 {
		b1, b2 := edid[offset], edid[offset+1]
		if (b1 == 1 && b2 == 1) || (b1 == 0 && b2 == 0) {
			continue
		}
		h := (int(b1) + 31) * 8
		v := [4]int{h * 10 / 16, h * 3 / 4, h * 4 / 5, h * 9 / 16}[b2>>6]
		hz := float64(b2&0x3f) + 60
		if tooFast(h, v, hz) {
			return fmt.Sprintf("%dx%d at %.0f Hz", h, v, hz), true
		}
	}
	for block := 1; block <= int(edid[126]) && (block+1)*128 <= len(edid); block++ {
		x := edid[block*128 : (block+1)*128]
		if x[0] != 0x02 {
			continue
		}
		end := int(x[2])
		if end < 4 || end > 127 {
			end = 4
		}
		for i := 4; i < end; {
			tag, length := x[i]>>5, int(x[i]&0x1f)
			if i+1+length > end {
				break
			}
			body := x[i+1 : i+1+length]
			switch {
			case tag == 2: // video data block
				for _, svd := range body {
					vic := svd
					if svd&0x80 != 0 && svd&0x7f >= 1 && svd&0x7f <= 64 {
						vic = svd & 0x7f // native flag
					}
					if cubeUnsafeVICs[vic] {
						return fmt.Sprintf("CTA VIC %d", vic), true
					}
				}
			case tag == 3 && length >= 8 && body[0] == 0x03 && body[1] == 0x0c && body[2] == 0x00:
				// HDMI VSDB: HDMI_VICs 1-4 are 3840x2160 and 4096x2160 modes.
				flags, j := body[7], 8
				if flags&0x80 != 0 {
					j += 2
				}
				if flags&0x40 != 0 {
					j += 2
				}
				if flags&0x20 != 0 && j+1 < length && body[j+1]>>5 != 0 {
					return "HDMI 4K VIC", true
				}
			case tag == 7 && length > 1 && body[0] == 14: // YCbCr 4:2:0 video data block
				return "YCbCr 4:2:0 4K mode", true
			}
			i += 1 + length
		}
		for offset := int(x[2]); offset >= 4 && offset+18 <= 127; offset += 18 {
			if mode, bad := detailed(x[offset : offset+18]); bad {
				return mode, true
			}
		}
	}
	return "", false
}
