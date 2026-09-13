package mjpeg

import "testing"

func TestSnapshotCannotMutateSharedDeliveryFrame(t *testing.T) {
	s := NewStreamer()
	s.enableLatestFrameCache()
	original := []byte{1, 2, 3}
	s.setLatestFrame(original, 640, 480)
	got, ok := s.getLatestFrame()
	if !ok {
		t.Fatal("missing snapshot")
	}
	got.Data[0] = 99
	again, _ := s.getLatestFrame()
	if original[0] != 1 || again.Data[0] != 1 {
		t.Fatal("snapshot modified shared image")
	}
	s.disableLatestFrameCache()
	if _, ok := s.getLatestFrame(); ok {
		t.Fatal("cache retained after disable")
	}
}

func BenchmarkCacheFrame(b *testing.B) {
	s := NewStreamer()
	frame := make([]byte, 256*1024)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s.setLatestFrame(frame, 2560, 1440)
	}
}
