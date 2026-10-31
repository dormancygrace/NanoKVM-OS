package notify

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
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
	s.dispatch(Event{Kind: KindSecurity, Message: "security.lockout", Vars: map[string]string{"failures": "5", "address": "10.0.0.1", "minutes": "5"}})
	s.dispatch(Event{Kind: KindSecurity, Message: "security.lockout", Vars: map[string]string{"failures": "5", "address": "10.0.0.2", "minutes": "5"}})
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

// A destination that hangs holds at most maxChannelDeliveries deliveries of
// its channel and maxDeliveries in all, retries included; distinct events
// beyond them are dropped, and the slots come back when the sends end.
func TestDeliveriesAreBounded(t *testing.T) {
	// One channel meets its own bound; eight meet the bound of all first.
	for _, tc := range []struct{ channels, peak, peakChannel int }{
		{1, maxChannelDeliveries, maxChannelDeliveries},
		{8, maxDeliveries, maxDeliveries / 8},
	} {
		t.Run(fmt.Sprint(tc.channels), func(t *testing.T) {
			boundedDeliveries(t, tc.channels, tc.peak, tc.peakChannel)
		})
	}
}

func boundedDeliveries(t *testing.T, count, wantPeak, wantPeakChannel int) {
	var channels []Channel
	for i := 0; i < count; i++ {
		channels = append(channels, Channel{ID: fmt.Sprint(i), Name: fmt.Sprint(i), Enabled: true, Events: []Kind{KindSecurity}})
	}
	s, _, _ := testService(t, channels...)
	release := make(chan struct{})
	var mu sync.Mutex
	current, peak, peakChannel, calls := 0, 0, 0, 0
	perChannel := map[string]int{}
	s.send = func(_ context.Context, ch Channel, _ Rendered) error {
		mu.Lock()
		calls++
		current++
		perChannel[ch.ID]++
		peak, peakChannel = max(peak, current), max(peakChannel, perChannel[ch.ID])
		mu.Unlock()
		<-release
		mu.Lock()
		current--
		perChannel[ch.ID]--
		mu.Unlock()
		return errors.New("timeout")
	}
	lockout := func(i int) Event {
		return Event{Kind: KindSecurity, Message: "security.lockout", Vars: map[string]string{"address": fmt.Sprintf("10.0.%d.%d", i/256, i%256)}}
	}
	for i := 0; i < 200; i++ {
		s.dispatch(lockout(i))
	}
	waitFor := func(what string, done func() bool) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); !done(); time.Sleep(time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal(what)
			}
		}
	}
	waitFor("blocked sends", func() bool { mu.Lock(); defer mu.Unlock(); return current == wantPeak })
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	if peak != wantPeak || peakChannel != wantPeakChannel {
		t.Fatalf("peak %d in all, %d per channel", peak, peakChannel)
	}
	mu.Unlock()
	// Retries keep their slots: the failed sends retry at once here.
	close(release)
	waitFor("slots freed", func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.active == 0 && len(s.inFlight) == 0 })
	sends := wantPeak * (1 + len(retryDelays))
	mu.Lock()
	if calls != sends || peak != wantPeak {
		t.Fatalf("%d sends (want %d), peak %d", calls, sends, peak)
	}
	mu.Unlock()
	s.dispatch(lockout(1000))
	waitFor("delivery after the slots came back", func() bool { mu.Lock(); defer mu.Unlock(); return calls > sends })
}

var lockoutVars = map[string]string{"failures": "5", "address": "10.0.0.1", "minutes": "15"}

func TestRenderLanguage(t *testing.T) {
	r := render(Event{Message: "security.lockout", Vars: lockoutVars, Details: "extra"}, "ru", nil)
	if r.Title != "Вход заблокирован" || r.Body != "5 неудачных входов с 10.0.0.1: вход с этого адреса заблокирован на 15 мин.\n\nextra" {
		t.Fatalf("%q %q", r.Title, r.Body)
	}
	if render(Event{Message: "power.on"}, "xx", nil).Title != "Host powered on" {
		t.Fatal("unknown language does not fall back to English")
	}
}

// Every language has every message, each with the variables of the English
// one and no others.
func TestMessageCatalogues(t *testing.T) {
	if len(messages) != len(Languages) {
		t.Fatalf("%d catalogues for %d languages", len(messages), len(Languages))
	}
	names := func(text string) []string {
		var found []string
		for _, match := range variable.FindAllStringSubmatch(text, -1) {
			found = append(found, match[1])
		}
		slices.Sort(found)
		return found
	}
	for language, catalogue := range messages {
		if len(catalogue) != len(MessageSpecs) {
			t.Errorf("%s: %d messages", language, len(catalogue))
		}
		for _, spec := range MessageSpecs {
			m, ok := catalogue[spec.Key]
			want := standardMessage("en", spec.Key)
			switch {
			case !ok || m.Title == "" || m.Body == "":
				t.Errorf("%s lacks %s", language, spec.Key)
			case !slices.Equal(names(m.Title+m.Body), names(want.Title+want.Body)):
				t.Errorf("%s %s: variables %v, want %v", language, spec.Key, names(m.Title+m.Body), names(want.Title+want.Body))
			case len(unknownVars(m.Title+m.Body, spec)) > 0:
				t.Errorf("%s %s: unknown variables", language, spec.Key)
			}
		}
	}
}

func TestTemplateRender(t *testing.T) {
	template := &Template{ID: "short", Name: "Short", Messages: map[string]Message{
		"security.lockout": {Title: "[{device}] blocked", Body: "{address} ({failures}x) for {minutes} min\n{details}\nend"},
		"power.off":        {Body: "off"},
	}}
	if err := template.validate(); err != nil {
		t.Fatal(err)
	}
	r := render(Event{Message: "security.lockout", Vars: lockoutVars, Details: "extra"}, "ru", template)
	if r.Title != "["+deviceName()+"] blocked" || r.Body != "10.0.0.1 (5x) for 15 min\nextra\nend" {
		t.Fatalf("%q %q", r.Title, r.Body)
	}
	// An empty title is the standard one; a message the template lacks is
	// the standard message of the language.
	if r := render(Event{Message: "power.off"}, "de", template); r.Body != "off" || r.Title != standardMessage("de", "power.off").Title {
		t.Fatalf("%q %q", r.Title, r.Body)
	}
	if r, std := render(Event{Message: "hdmi.lost"}, "de", template), render(Event{Message: "hdmi.lost"}, "de", nil); r.Title != std.Title || r.Body != std.Body {
		t.Fatal("a message the template lacks is not the standard one")
	}
	// Unknown braces stay as written.
	if got := fill("{a} {user} {", map[string]string{"user": "x"}); got != "{a} x {" {
		t.Fatal(got)
	}
}

func TestTemplateValidation(t *testing.T) {
	for name, tc := range map[string]Template{
		"no name":            {ID: "a", Messages: map[string]Message{}},
		"bad id":             {ID: "A B", Name: "x"},
		"unknown message":    {ID: "a", Name: "x", Messages: map[string]Message{"power.of": {Body: "x"}}},
		"unknown variable":   {ID: "a", Name: "x", Messages: map[string]Message{"power.off": {Body: "{user} turned it off"}}},
		"another's variable": {ID: "a", Name: "x", Messages: map[string]Message{"hdmi.lost": {Title: "{address}"}}},
		"two-line title":     {ID: "a", Name: "x", Messages: map[string]Message{"hdmi.lost": {Title: "a\nb"}}},
		"long body":          {ID: "a", Name: "x", Messages: map[string]Message{"hdmi.lost": {Body: strings.Repeat("x", maxBodyBytes+1)}}},
	} {
		if err := tc.validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	c := Config{Language: "en", Templates: []Template{{ID: "a", Name: "x"}},
		Channels: []Channel{{ID: "c", Type: "slack", Settings: map[string]string{"url": "https://example.com/h"}, Template: "b"}}}
	if err := c.Validate(); err == nil {
		t.Fatal("a channel with a missing template accepted")
	}
	c.Channels[0].Template = "a"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Templates = append(c.Templates, Template{ID: "b", Name: "X"})
	if err := c.Validate(); err == nil {
		t.Fatal("two templates of one name accepted")
	}
}

// Each channel gets the event in its own template.
func TestDispatchUsesTheChannelsTemplate(t *testing.T) {
	s, rec, _ := testService(t,
		Channel{ID: "1", Name: "plain", Enabled: true, Events: []Kind{KindPower}},
		Channel{ID: "2", Name: "short", Enabled: true, Events: []Kind{KindPower}, Template: "t"},
	)
	s.config.Templates = []Template{{ID: "t", Name: "Short", Messages: map[string]Message{"power.off": {Title: "OFF"}}}}
	s.dispatch(Event{Kind: KindPower, Message: "power.off"})
	sent := rec.wait(t, 2)
	slices.Sort(sent)
	if !slices.Equal(sent, []string{"plain:Host powered off", "short:OFF"}) {
		t.Fatal(sent)
	}
}

func TestPreview(t *testing.T) {
	s, _, _ := testService(t)
	previews, err := s.Preview(PreviewRequest{Language: "ru", Template: &Template{Name: "x",
		Messages: map[string]Message{"security.new-address": {Body: "{user}@{address}"}}}})
	if err != nil || len(previews) != len(MessageSpecs) {
		t.Fatal(previews, err)
	}
	for _, p := range previews {
		switch p.Key {
		case "security.new-address":
			if p.Body != "admin@203.0.113.7" || p.Title != "Вход с нового адреса" || !strings.HasPrefix(p.Text, p.Body+"\n\n") {
				t.Fatalf("%+v", p)
			}
		case "screen.panic":
			if !strings.Contains(p.Body, "Kernel panic - not syncing") {
				t.Fatalf("%+v", p)
			}
		}
		if strings.Contains(p.Title+p.Body, "{") {
			t.Fatalf("unfilled: %+v", p)
		}
	}
	if _, err := s.Preview(PreviewRequest{Language: "ru", Template: &Template{Name: "x",
		Messages: map[string]Message{"power.on": {Body: "{nope}"}}}}); err == nil {
		t.Fatal("an unknown variable previewed")
	}
}
