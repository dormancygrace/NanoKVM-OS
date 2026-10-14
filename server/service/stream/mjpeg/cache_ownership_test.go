package mjpeg

import "testing"

func BenchmarkCacheFrame(b *testing.B) {
	s := NewStreamer()
	frame := make([]byte, 256*1024)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s.setLatestFrame(frame, 2560, 1440)
	}
}
