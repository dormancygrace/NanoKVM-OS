package download

import (
	"testing"
	"time"
)

func TestTransferMeterResumeUnknownAndStall(t *testing.T) {
	start := time.Unix(10, 0)
	meter := &transferMeter{previousBytes: 1000, previousTime: start}
	p := meter.sample(1400, 2000, start.Add(2*time.Second))
	if p.BytesPerSecond != 200 || p.percentage() != "70.00%" {
		t.Fatalf("resumed bytes counted as transfer: %+v", p)
	}
	p = meter.sample(1400, 0, start.Add(3*time.Second))
	if p.BytesPerSecond != 0 || p.percentage() != "" {
		t.Fatalf("stalled unknown-length transfer: %+v", p)
	}
	p = meter.sample(1600, -1, start.Add(4*time.Second))
	if p.BytesPerSecond != 200 || p.Total != 0 {
		t.Fatalf("unknown size lost speed: %+v", p)
	}
}
