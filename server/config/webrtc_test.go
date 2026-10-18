package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateWebRTCKeepsOtherSettings(t *testing.T) {
	original, saved := configurationFile, instance
	configurationFile = filepath.Join(t.TempDir(), "server.yaml")
	once.Do(func() {}) // the test owns the instance
	t.Cleanup(func() { configurationFile, instance = original, saved })

	existing := `proto: https
# kept comment
stun: stun.l.google.com:19302
customKey: kept
`
	if err := os.WriteFile(configurationFile, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := WebRTCSettings().DTLSCipher; got != DTLSChaCha20 {
		t.Fatalf("default cipher %q", got)
	}
	want := WebRTC{Stun: "a.example:3478,b.example:3478",
		Turn:       Turn{TurnAddr: "turn.example:3478", TurnUser: "alice", TurnCred: "secret"},
		DTLSCipher: DTLSAESGCM}
	if err := UpdateWebRTC(want); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(configurationFile)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, s := range []string{"# kept comment", "customKey: kept", "proto: https", "stun: a.example:3478,b.example:3478",
		"turnAddr: turn.example:3478", "turnUser: alice", "turnCred: secret", "dtlsCipher: aes-gcm"} {
		if !strings.Contains(text, s) {
			t.Errorf("missing %q in\n%s", s, text)
		}
	}
	if info, err := os.Stat(configurationFile); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, %v", info.Mode().Perm(), err)
	}
	if got := WebRTCSettings(); got != want {
		t.Fatalf("in use: %+v", got)
	}
	// The default set is stored as an empty value; an unknown cipher is ChaCha20.
	if err := UpdateWebRTC(WebRTC{DTLSCipher: "rot13"}); err != nil {
		t.Fatal(err)
	}
	if got := WebRTCSettings(); got.Stun != DefaultStun || got.Turn != (Turn{}) || got.DTLSCipher != DTLSChaCha20 {
		t.Fatalf("defaults: %+v", got)
	}
}
