package common

// One allocation belongs to Go for the full asynchronous consumer lifetime.
// No encoder/DMA pointer escapes the synchronous callback.
type videoPackStorage struct {
	storage               []byte
	headroom, total, next int
	failed                bool
}

func (s *videoPackStorage) appendPack(pack []byte, offset, total int) bool {
	const maxFrame = 64 << 20
	if s.failed || total <= 0 || total > maxFrame || s.headroom < 0 || s.headroom > maxFrame || offset != s.next || len(pack) == 0 || offset > total || len(pack) > total-offset {
		s.failed = true
		return false
	}
	if s.storage == nil {
		s.total = total
		s.storage = make([]byte, s.headroom+total)
	}
	if s.total != total {
		s.failed = true
		return false
	}
	copy(s.storage[s.headroom+offset:], pack)
	s.next += len(pack)
	return true
}
func (s *videoPackStorage) complete() bool { return !s.failed && s.total > 0 && s.next == s.total }

// Native result codes are kept; a successful read without a complete access
// unit is reported as -2 (VENC error). Both capture paths share this mapping.
func (s *videoPackStorage) videoResult(result int) ([]byte, []byte, int) {
	if result < 0 {
		return nil, nil, result
	}
	if !s.complete() {
		return nil, nil, -2
	}
	return s.storage, s.storage[s.headroom:], result
}

// Nonzero MJPEG results, including 5 (frame unchanged), carry no image.
func (s *videoPackStorage) mjpegResult(result int) ([]byte, int) {
	if result != 0 {
		return nil, result
	}
	if !s.complete() {
		return nil, -2
	}
	return s.storage, result
}
