package notify

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type recorder struct {
	mu    sync.Mutex
	sent  []string
	fails map[string]int
	done  chan struct{}
}

func (r *recorder) send(_ context.Context, ch Channel, rendered Rendered) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, ch.Name+":"+rendered.Title)
	defer func() { r.done <- struct{}{} }()
	if r.fails[ch.Name] > 0 {
		r.fails[ch.Name]--
		if ch.Name == "broken" {
			return permanent(errors.New("bad token"))
		}
		return errors.New("timeout")
	}
	return nil
}

func testService(t *testing.T, channels ...Channel) (*Service, *recorder, *time.Time) {
	t.Helper()
	s := newService(filepath.Join(t.TempDir(), "config.json"))
	s.config = Config{Language: "en", Channels: channels}
	rec := &recorder{fails: map[string]int{}, done: make(chan struct{}, 16)}
	now := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	s.send, s.now = rec.send, func() time.Time { return now }
	s.sleep = func(context.Context, time.Duration) bool { return true }
	return s, rec, &now
}

func (r *recorder) wait(t *testing.T, n int) []string {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-r.done:
		case <-time.After(5 * time.Second):
			t.Fatalf("%d of %d sends", i, n)
		}
	}
	select {
	case <-r.done:
		t.Fatal("more sends than expected")
	case <-time.After(50 * time.Millisecond):
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	sent := r.sent
	r.sent = nil
	return sent
}

func TestDispatchFollowsKindsAndRepeats(t *testing.T) {
	s, rec, now := testService(t,
		Channel{Name: "power", Enabled: true, Events: []Kind{KindPower}},
		Channel{Name: "off", Enabled: false, Events: []Kind{KindPower}},
		Channel{Name: "security", Enabled: true, Events: []Kind{KindSecurity}},
	)
	s.dispatch(Event{Kind: KindPower, Message: "power.off"})
	if sent := rec.wait(t, 1); sent[0] != "power:Host powered off" {
		t.Fatal(sent)
	}
	// The same event again within the minute is dropped; other arguments pass.
	s.dispatch(Event{Kind: KindPower, Message: "power.off"})
	s.dispatch(Event{Kind: KindSecurity, Message: "security.lockout", Args: []any{5, "10.0.0.1", 5}})
	s.dispatch(Event{Kind: KindSecurity, Message: "security.lockout", Args: []any{5, "10.0.0.2", 5}})
	rec.wait(t, 2)
	*now = now.Add(repeatWindow)
	s.dispatch(Event{Kind: KindPower, Message: "power.off"})
	rec.wait(t, 1)
}

func TestDeliverRetries(t *testing.T) {
	s, rec, _ := testService(t,
		Channel{Name: "flaky", Enabled: true, Events: []Kind{KindHDMI}},
		Channel{Name: "broken", Enabled: true, Events: []Kind{KindHDMI}},
	)
	rec.fails["flaky"] = 2
	rec.fails["broken"] = 5
	s.dispatch(Event{Kind: KindHDMI, Message: "hdmi.lost"})
	// flaky: two failures and a success; broken: one permanent failure.
	rec.wait(t, 4)

	rec.fails["flaky"] = 10
	s.dispatch(Event{Kind: KindHDMI, Message: "hdmi.restored"})
	// Gives up after the last retry: 1 + len(retryDelays) attempts, plus broken's one.
	rec.wait(t, 1+len(retryDelays)+1)
}

func TestRenderLanguage(t *testing.T) {
	r := render(Event{Message: "security.lockout", Args: []any{5, "10.0.0.1", 15}, Details: "extra"}, "ru")
	if r.Title != "Вход заблокирован" || r.Body != "5 неудачных входов с 10.0.0.1: вход с этого адреса заблокирован на 15 мин.\n\nextra" {
		t.Fatalf("%q %q", r.Title, r.Body)
	}
	if render(Event{Message: "power.on"}, "xx").Title != "Host powered on" {
		t.Fatal("unknown language does not fall back to English")
	}
	for language, catalogue := range messages {
		for key := range messages["en"] {
			if _, ok := catalogue[key]; !ok {
				t.Errorf("%s lacks %s", language, key)
			}
		}
	}
}
