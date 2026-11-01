package middleware

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"NanoKVM-Server/authn"
)

// A session whose token is still good survives the recheck.
func TestSessionRecheckKeepsValidSessions(t *testing.T) {
	store := authn.NewStore(filepath.Join(t.TempDir(), "pwd"))
	user, ok, err := store.Authenticate("admin", "admin")
	if err != nil || !ok {
		t.Fatalf("default login: ok=%v err=%v", ok, err)
	}
	registry := newSessionRegistry(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	unregister := registry.register(session{
		username:     user.Username,
		tokenVersion: user.TokenVersion,
		store:        store,
		cancel:       cancel,
	})
	defer unregister()

	registry.recheck()
	if ctx.Err() != nil {
		t.Fatal("valid session was cancelled")
	}

	if _, err = store.Revoke(user.Username); err != nil {
		t.Fatal(err)
	}
	registry.recheck()
	if ctx.Err() == nil {
		t.Fatal("revoked session survived the recheck")
	}
}
