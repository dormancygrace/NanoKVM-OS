package picoclaw

import "testing"

func TestMediaKeysAreUnsupported(t *testing.T) {
	for _, key := range []string{"MediaPlayPause", "BrowserSearch", "LaunchApp2", "Sleep"} {
		if _, _, _, err := resolveKey(key); err == nil {
			t.Errorf("%s resolved, but the keyboard cannot send it", key)
		}
	}
	if _, code, _, err := resolveKey("AudioVolumeUp"); err != nil || code != 0x80 {
		t.Errorf("AudioVolumeUp = %#x, %v", code, err)
	}
}
