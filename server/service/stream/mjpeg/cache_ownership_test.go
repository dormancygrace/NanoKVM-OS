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

func TestLatestFrameCacheClearsAfterLastRelease(t *testing.T) {
	s := NewStreamer()
	s.enableLatestFrameCache()
	s.enableLatestFrameCache()
	s.setLatestFrame([]byte{1, 2, 3}, 640, 480)
	s.disableLatestFrameCache()
	if !s.frameCacheEnabled() || len(s.latestFrame.Data) != 3 {
		t.Fatal("first release cleared a cache still in use")
	}
	s.disableLatestFrameCache()
	if s.frameCacheEnabled() || len(s.latestFrame.Data) != 0 || s.latestFrame.Width != 0 || s.latestFrame.Height != 0 || !s.latestFrame.CapturedAt.IsZero() {
		t.Fatal("last release retained cached data or metadata")
	}
	s.disableLatestFrameCache()
	s.enableLatestFrameCache()
	if !s.frameCacheEnabled() || len(s.latestFrame.Data) != 0 {
		t.Fatal("extra release underflowed references or revived the old frame")
	}
	s.disableLatestFrameCache()
}
