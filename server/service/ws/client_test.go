package ws

import (
	"testing"
)

func TestMouseReportStartsCooldown(t *testing.T) {
	tests := []struct {
		name   string
		report []byte
		want   bool
	}{
		{name: "relative move", report: []byte{0, 10, 0, 0}, want: false},
		{name: "relative wheel", report: []byte{0, 0, 0, 1}, want: true},
		{name: "relative button", report: []byte{1, 0, 0, 0}, want: true},
		{name: "absolute move", report: []byte{0, 1, 0, 1, 0, 0}, want: false},
		{name: "absolute wheel", report: []byte{0, 1, 0, 1, 0, 0xff}, want: true},
		{name: "absolute button", report: []byte{1, 1, 0, 1, 0, 0}, want: true},
		{name: "invalid", report: []byte{0, 1}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mouseReportStartsCooldown(tt.report); got != tt.want {
				t.Fatalf("mouseReportStartsCooldown(%v) = %v, want %v", tt.report, got, tt.want)
			}
		})
	}
}
