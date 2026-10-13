package mjpeg

import (
	"testing"
	"time"
)

// run feeds ticks at 60 fps for d, busy as given, and returns the quality.
func run(a *adaptiveQuality, ceiling int, busy bool, start time.Time, d time.Duration) (int, time.Time) {
	q := 0
	now := start
	for end := start.Add(d); now.Before(end); now = now.Add(time.Second / 60) {
		q = a.next(ceiling, busy, now)
	}
	return q, now
}

func TestAdaptiveQualityFollowsTheViewers(t *testing.T) {
	var a adaptiveQuality
	now := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	q, now := run(&a, 80, false, now, time.Second)
	if q != 80 {
		t.Fatalf("keeping up: quality %d, want the ceiling 80", q)
	}
	// Viewers that cannot keep up push the quality down to the floor.
	q, now = run(&a, 80, true, now, 2*time.Second)
	if q >= 60 {
		t.Fatalf("after 2 s busy: quality %d, want well below 80", q)
	}
	q, now = run(&a, 80, true, now, 5*time.Second)
	if q != minAdaptiveQuality {
		t.Fatalf("busy: quality %d, want the floor %d", q, minAdaptiveQuality)
	}
	// Once they keep up (or the screen is still), it climbs back.
	q, _ = run(&a, 80, false, now, 6*time.Second)
	if q != 80 {
		t.Fatalf("recovered: quality %d, want 80", q)
	}
}

func TestAdaptiveQualityHoldsInBetween(t *testing.T) {
	a := adaptiveQuality{quality: 60}
	now := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	// One busy tick in six: neither congested nor free.
	for i := 0; i < 600; i++ {
		if q := a.next(80, i%6 == 0, now); q != 60 {
			t.Fatalf("tick %d: quality %d, want it held at 60", i, q)
		}
		now = now.Add(time.Second / 60)
	}
}

func TestAdaptiveQualityFollowsTheSetting(t *testing.T) {
	a := adaptiveQuality{quality: 80}
	now := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	if q := a.next(50, false, now); q != 50 {
		t.Fatalf("lowered setting: quality %d, want 50", q)
	}
	// A setting below the floor is kept as it is.
	b := adaptiveQuality{}
	q, _ := run(&b, 25, true, now, 3*time.Second)
	if q != 25 {
		t.Fatalf("setting 25: quality %d", q)
	}
}
