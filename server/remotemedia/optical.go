package remotemedia

import "errors"

const MaxOpticalSize uint64 = (0x1000000 - 0x030000) * 2048
const LegacyOpticalSize uint64 = (256*60*75 - 1) * 2048

// ValidateOptical matches the gadget's optical address range. The capability
// is detected from configfs, not guessed from the kernel release string.
func ValidateOptical(size uint64, dvdSupported bool) error {
	if err := ValidateImage(size); err != nil {
		return err
	}
	if size < 300*2048 {
		return errors.New("CD/DVD images must be at least 600 KiB")
	}
	if size > MaxOpticalSize {
		return errors.New("CD/DVD images cannot exceed 31.625 GiB")
	}
	if !dvdSupported && size > LegacyOpticalSize {
		return errors.New("Update the device kernel to enable large DVD images")
	}
	return nil
}
