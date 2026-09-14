package download

import (
	"fmt"
	"sync"
	"time"
)

type transferProgress struct {
	Bytes          int64
	Total          int64
	BytesPerSecond float64
}

func (p transferProgress) percentage() string {
	if p.Total <= 0 {
		return ""
	}
	percent := float64(p.Bytes) / float64(p.Total) * 100
	if percent > 100 {
		percent = 100
	}
	if percent < 0 {
		percent = 0
	}
	return fmt.Sprintf("%.2f%%", percent)
}

type transferMeter struct {
	mu            sync.Mutex
	previousBytes int64
	previousTime  time.Time
}

func newTransferMeter(offset int64) *transferMeter {
	return &transferMeter{previousBytes: offset, previousTime: time.Now()}
}
func (m *transferMeter) sample(bytes, total int64, now time.Time) transferProgress {
	m.mu.Lock()
	defer m.mu.Unlock()
	speed := float64(0)
	if elapsed := now.Sub(m.previousTime).Seconds(); elapsed > 0 && bytes >= m.previousBytes {
		speed = float64(bytes-m.previousBytes) / elapsed
	}
	m.previousBytes, m.previousTime = bytes, now
	if total < 0 {
		total = 0
	}
	return transferProgress{Bytes: bytes, Total: total, BytesPerSecond: speed}
}
