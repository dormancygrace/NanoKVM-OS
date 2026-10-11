package middleware

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"NanoKVM-Server/authn"
)

// One goroutine rechecks every running session, and it goes away with the
// last of them rather than lingering for the life of the server.
func TestSessionRecheckStopsWithTheLastSession(t *testing.T) {
	store := authn.NewStore(filepath.Join(t.TempDir(), "pwd"))
	user, ok, err := store.Authenticate("admin", "admin")
	if err != nil || !ok {
		t.Fatalf("default login: ok=%v err=%v", ok, err)
	}

	registry := newSessionRegistry(10 * time.Millisecond)
	var unregisters []func()
	for range 3 {
		_, cancel := context.WithCancel(context.Background())
		defer cancel()
		unregisters = append(unregisters, registry.register(session{
			username:     user.Username,
			tokenVersion: user.TokenVersion,
			store:        store,
			cancel:       cancel,
		}))
	}
	for _, unregister := range unregisters {
		unregister()
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		registry.mutex.Lock()
		watching := registry.watching
		registry.mutex.Unlock()
		if !watching {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("recheck goroutine outlived the last session")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

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
