package rustdesk

import "testing"

func TestRelativeUSBPointerKeepsMotionButtonsAndWheel(t *testing.T) {
	p := relativePointer{width: 1920, height: 1080}
	first := p.reports(0, 0, 0, 0)
	if len(first) != 1 || first[0][1] != 0 || first[0][2] != 0 {
		t.Fatal(first)
	}
	reports := p.reports(1, 32767, 32767, -1)
	var x, y, wheel int
	for _, r := range reports {
		if len(r) != 5 || r[0] != 1 {
			t.Fatal(r)
		}
		x += int(int8(r[1]))
		y += int(int8(r[2]))
		wheel += int(int8(r[3]))
	}
	if x != 1920 || y != 1080 || wheel != -1 {
		t.Fatalf("lost movement: %d %d wheel=%d", x, y, wheel)
	}
	reports = p.reports(0, 0, 0, 0)
	x, y = 0, 0
	for _, r := range reports {
		if r[0] != 0 {
			t.Fatal("release lost", r)
		}
		x += int(int8(r[1]))
		y += int(int8(r[2]))
	}
	if x != -1920 || y != -1080 {
		t.Fatalf("negative movement lost: %d %d", x, y)
	}
}

func TestRelativeUSBPointerAccumulatesSmallMoves(t *testing.T) {
	p := relativePointer{width: 1920, height: 1080}
	p.reports(0, 0, 0, 0)
	var x, y int
	for i := int32(1); i <= 100; i++ {
		for _, r := range p.reports(0, i, i, 0) {
			x += int(int8(r[1]))
			y += int(int8(r[2]))
		}
	}
	if x != int(100*1920/32767) || y != int(100*1080/32767) {
		t.Fatalf("fractional motion lost: %d %d", x, y)
	}
}
