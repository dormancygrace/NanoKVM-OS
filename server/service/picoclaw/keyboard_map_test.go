package picoclaw

import "testing"

// The keyboard descriptor ends its key array at usage 0xE7; a host drops any
// larger code, so a key mapped there would be accepted and do nothing.
func TestKeycodesFitTheKeyboardDescriptor(t *testing.T) {
	for name, code := range keycodeMap {
		if code > 0xe7 {
			t.Errorf("%s = %#x, above the keyboard usage range", name, code)
		}
	}
}

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
