package rustdesk

// The Android client sends normalized absolute positions even in trackpad
// mode. When USB exposes only a relative mouse, preserve successive movement
// in screen pixels, fractional displacement, buttons, and one wheel event.
type relativePointer struct {
	known                  bool
	x, y                   int32
	xRemainder, yRemainder int64
	width, height          int32
}

func (p *relativePointer) reports(buttons byte, x, y int32, wheel int8) [][]byte {
	var dx, dy int64
	if p.known {
		nx := int64(x-p.x)*int64(p.width) + p.xRemainder
		ny := int64(y-p.y)*int64(p.height) + p.yRemainder
		dx, dy = nx/32767, ny/32767
		p.xRemainder, p.yRemainder = nx%32767, ny%32767
	}
	p.x, p.y, p.known = x, y, true
	clamp := func(v int64) int64 {
		if v > 127 {
			return 127
		}
		if v < -127 {
			return -127
		}
		return v
	}
	var reports [][]byte
	for {
		xStep, yStep := clamp(dx), clamp(dy)
		reports = append(reports, []byte{buttons, byte(int8(xStep)), byte(int8(yStep)), byte(wheel), 0})
		dx, dy, wheel = dx-xStep, dy-yStep, 0
		if dx == 0 && dy == 0 {
			return reports
		}
	}
}
