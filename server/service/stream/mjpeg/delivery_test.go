package mjpeg

import (
	"context"
	"testing"
	"time"
)

func TestDuplicateDeliveryIncludesNewViewerAndPeriodicRefresh(t *testing.T) {
	d := frameDelivery{}
	first := newMjpegClient(context.Background())
	now := time.Unix(100, 0)
	a := []byte("jpeg-A")
	if !d.offer([]*mjpegClient{first}, a, now) {
		t.Fatal("first frame absent")
	}
	first.next()
	if d.offer([]*mjpegClient{first}, append([]byte(nil), a...), now.Add(time.Second)) {
		t.Fatal("identical frame resent")
	}
	second := newMjpegClient(context.Background())
	if !d.offer([]*mjpegClient{first, second}, a, now.Add(2*time.Second)) {
		t.Fatal("new viewer missing still frame")
	}
	if len(first.frames) != 0 || len(second.frames) != 1 {
		t.Fatal("new viewer delivery affected existing viewer")
	}
	second.next()
	if !d.offer([]*mjpegClient{first, second}, a, now.Add(duplicateRefreshInterval)) {
		t.Fatal("refresh absent")
	}
	if len(first.frames) != 1 || len(second.frames) != 0 {
		t.Fatal("refresh clock was shared between clients")
	}
	first.next()
	if !d.offer([]*mjpegClient{first, second}, []byte("jpeg-B"), now.Add(6*time.Second)) {
		t.Fatal("same-length changed frame suppressed")
	}
	for _, c := range []*mjpegClient{first, second} {
		data, _ := c.next()
		if string(data) != "jpeg-B" {
			t.Fatal("changed frame missing")
		}
	}
	d.last = nil
	if !d.offer([]*mjpegClient{first, second}, []byte("jpeg-B"), now.Add(7*time.Second)) {
		t.Fatal("recovery frame suppressed")
	}
}

func TestBackpressureRequiresAllActiveViewersToBeFull(t *testing.T) {
	a := newMjpegClient(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	b := newMjpegClient(ctx)
	clients := []*mjpegClient{a, b}
	a.offer([]byte("old"))
	if !viewersReady(clients) {
		t.Fatal("slow client blocked ready peer")
	}
	b.offer([]byte("old"))
	if viewersReady(clients) {
		t.Fatal("read with every queue full")
	}
	b.next()
	cancel()
	if viewersReady(clients) {
		t.Fatal("cancelled client demanded capture")
	}
	a.next()
	if !viewersReady(clients) {
		t.Fatal("drained peer did not resume capture")
	}
}

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
