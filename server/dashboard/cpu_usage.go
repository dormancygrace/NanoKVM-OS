package dashboard

import (
	"sync"
	"time"
)

// Sample independently of browser requests so the first dashboard response
// includes a recent interval, rather than a since-boot average.
type cpuSampler struct {
	mu       sync.Mutex
	previous *CPU
	usage    *float64
	sampled  time.Time
}

func (s *cpuSampler) update(next *CPU, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usage = nil
	last := s.previous
	if next != nil && last != nil && next.Total > last.Total && next.Idle >= last.Idle {
		total, idle := next.Total-last.Total, next.Idle-last.Idle
		if idle <= total {
			value := float64(total-idle) * 100 / float64(total)
			s.usage = &value
		}
	}
	s.previous, s.sampled = next, now
}

func (s *cpuSampler) value(now time.Time) *float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.usage == nil || now.Sub(s.sampled) > 3*time.Second {
		return nil
	}
	value := *s.usage
	return &value
}

var dashboardCPU cpuSampler

func init() {
	dashboardCPU.update(parseCPU(read("/proc/stat")), time.Now())
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			dashboardCPU.update(parseCPU(read("/proc/stat")), time.Now())
		}
	}()
}
