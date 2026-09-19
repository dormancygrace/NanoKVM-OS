package dashboard

import (
	"testing"
	"time"
)

func TestCPUUsageSample(t *testing.T) {
	var s cpuSampler
	now := time.Now()
	s.update(&CPU{Total: 100, Idle: 60}, now)
	if s.value(now) != nil {
		t.Fatal("one sample cannot measure usage")
	}
	s.update(&CPU{Total: 200, Idle: 130}, now.Add(time.Second))
	value := s.value(now.Add(time.Second))
	if value == nil || *value != 30 {
		t.Fatalf("want 30%%, got %v", value)
	}
	if s.value(now.Add(5*time.Second)) != nil {
		t.Fatal("stale usage must not be returned")
	}
	for _, next := range []*CPU{{Total: 1, Idle: 1}, nil, {Total: 100, Idle: 10}, {Total: 101, Idle: 20}} {
		s.update(next, now)
		if s.value(now) != nil {
			t.Fatal("invalid/reset counters must not produce usage")
		}
	}
	s.update(&CPU{Total: 201, Idle: 70}, now)
	if value := s.value(now); value == nil || *value != 50 {
		t.Fatal("sampler did not recover")
	}
}
