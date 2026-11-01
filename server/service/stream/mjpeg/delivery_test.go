package mjpeg

import (
	"context"
	"testing"
	"time"
)

func TestNativeJpegCounterDoesNotForceDelivery(t *testing.T) {
	a := []byte{0xff, 0xd8, 0xff, 0xe9, 0x00, 0x04, 0x01, 0xff, 0xff, 0xdb, 0x01, 0x02}
	b := append([]byte(nil), a...)
	b[6], b[7] = 0x02, 0x00
	if !sameMjpegImage(a, b) {
		t.Fatal("native sequence counter treated as image change")
	}
	d := frameDelivery{}
	c := newMjpegClient(context.Background())
	now := time.Unix(100, 0)
	d.offer([]*mjpegClient{c}, a, now)
	c.next()
	if d.offer([]*mjpegClient{c}, b, now.Add(time.Second)) {
		t.Fatal("counter-only frame resent")
	}
	b[10]++
	if !d.offer([]*mjpegClient{c}, b, now.Add(2*time.Second)) {
		t.Fatal("coding-table change suppressed")
	}
	for _, index := range []int{0, 2, 3, 4, 5, 8, 11} {
		other := append([]byte(nil), a...)
		other[index]++
		if sameMjpegImage(a, other) {
			t.Fatalf("ignored byte outside counter at %d", index)
		}
	}
	if sameMjpegImage(a, a[:len(a)-1]) {
		t.Fatal("size change suppressed")
	}
	// A different APP marker is not the hardware sequence-counter convention.
	a[3], b[3] = 0xe1, 0xe1
	if sameMjpegImage(a, b) {
		t.Fatal("arbitrary APP payload ignored")
	}
	if sameMjpegImage([]byte{1, 2, 3}, []byte{1, 2, 4}) {
		t.Fatal("short input mismatch ignored")
	}
}
