package stream

import "testing"

func TestRateWindowAverages(t *testing.T) {
	var w rateWindow
	// The first seconds average what there is.
	for i, c := range []struct{ count, want int64 }{{90, 90}, {60, 75}, {61, 70}} {
		if got := w.add(c.count); got != c.want {
			t.Fatalf("second %d: add(%d) = %d, want %d", i, c.count, got, c.want)
		}
	}
	w.add(90)
	if got := w.add(90); got != 78 { // (90+60+61+90+90)/5 = 78.2
		t.Fatalf("full window = %d, want 78", got)
	}
	// The oldest second drops out: (60+61+90+90+90)/5 = 78.2, then 84.2, 90.
	if got := w.add(90); got != 78 {
		t.Fatalf("after one shift = %d, want 78", got)
	}
	if got := w.add(90); got != 84 {
		t.Fatalf("after two shifts = %d, want 84", got)
	}
	if got := w.add(90); got != 90 {
		t.Fatalf("after three shifts = %d, want 90", got)
	}
	// Rounded, not truncated: 89.6 is 90.
	var r rateWindow
	for _, c := range []int64{90, 90, 90, 89, 89} {
		r.add(c)
	}
	if got := r.add(90); got != 90 {
		t.Fatalf("rounding = %d, want 90", got)
	}
}
