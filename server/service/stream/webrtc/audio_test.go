package webrtc

import (
	"testing"
)

func TestAudioDelayBounds(t *testing.T) {
	for _, c := range []struct {
		value string
		want  int64
	}{{"", 0}, {"x", 0}, {"-5", 0}, {"80", 80}, {"9999", 500}} {
		t.Setenv("NANOKVM_WEBRTC_AUDIO_DELAY_MS", c.value)
		if got := audioDelay().Milliseconds(); got != c.want {
			t.Errorf("delay %q = %d ms, want %d", c.value, got, c.want)
		}
	}
}
