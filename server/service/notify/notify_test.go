package notify

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

var lockoutVars = map[string]string{"failures": "5", "address": "10.0.0.1", "minutes": "15"}

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
	// A variable with line breaks keeps the title on one line.
	newAddress := &Template{ID: "n", Name: "n", Messages: map[string]Message{"security.new-address": {Title: "{user} in"}}}
	if r := render(Event{Message: "security.new-address", Vars: map[string]string{"user": "a\r\nb", "address": "x"}}, "en", newAddress); r.Title != "a b in" {
		t.Fatalf("%q", r.Title)
	}
	// Unknown braces stay as written.
	if got := fill("{a} {user} {", map[string]string{"user": "x"}); got != "{a} x {" {
		t.Fatal(got)
	}
}

// Lockouts of many addresses send one message, then one with the count of
// the rest, and the host's events still go out meanwhile.
func TestLockoutsCoalesce(t *testing.T) {
	for _, message := range []string{"security.lockout", "security.attempt-limit"} {
		t.Run(message, func(t *testing.T) {
			s := newService(t.TempDir() + "/config.json")
			s.config.Channels = []Channel{{ID: "a", Type: "webhook", Enabled: true, Events: []Kind{KindSecurity, KindPower}}}
			sent := make(chan string, 16)
			s.send = func(_ context.Context, _ Channel, r Rendered) error {
				sent <- string(r.Kind) + ": " + r.Body
				return nil
			}
			start := time.Now()
			s.now = func() time.Time { return start }
			for i := 0; i <= 10; i++ {
				if i == 10 {
					s.dispatch(Event{Kind: KindPower, Message: "power.off"})
					s.now = func() time.Time { return start.Add(11 * time.Minute) }
				}
				s.dispatch(Event{Kind: KindSecurity, Message: message, Vars: map[string]string{"address": strconv.Itoa(i)}})
			}
			time.Sleep(200 * time.Millisecond)
			bodies := []string{<-sent, <-sent, <-sent}
			slices.Sort(bodies)
			if len(sent) > 0 || !strings.HasPrefix(bodies[0], "power: ") || !strings.HasSuffix(bodies[2], "9 more addresses blocked since "+start.Format("15:04")+".") {
				t.Fatalf("%q, %d more", bodies, len(sent))
			}
		})
	}
}

// A retry after the channel became Slack is escaped for Slack.
func TestRetryEscapesForTheChannelOfNow(t *testing.T) {
	s := newService(t.TempDir() + "/config.json")
	s.config.Channels = []Channel{{ID: "a", Type: "webhook", Enabled: true, Events: []Kind{KindScreen}}}
	var bodies []string
	s.send = func(_ context.Context, ch Channel, r Rendered) error {
		bodies = append(bodies, ch.Type+": "+r.Body)
		if ch.Type == "webhook" {
			return errors.New("500")
		}
		return nil
	}
	s.sleep = func(context.Context, time.Duration) bool {
		s.config.Channels[0].Type = "slack"
		return true
	}
	s.active[0], s.inFlight["a"] = 1, 1
	s.deliver(s.config.Channels[0], Event{Kind: KindScreen, Message: "screen.bsod", Details: "<!channel>"})
	if len(bodies) != 2 || !strings.Contains(bodies[0], "<!channel>") || strings.Contains(bodies[1], "<!channel>") {
		t.Fatalf("%q", bodies)
	}
}

// Login events to every channel leave the host's events a slot on each.
func TestLoginFloodLeavesHostSlots(t *testing.T) {
	s := newService(t.TempDir() + "/config.json")
	for i := range maxChannels {
		id := strconv.Itoa(i)
		for range maxChannelDeliveries {
			s.admit(id, KindSecurity)
		}
	}
	for i := range maxChannels {
		if !s.admit(strconv.Itoa(i), KindPower) {
			t.Fatalf("channel %d has no slot for a power event", i)
		}
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
		"details in a title": {ID: "a", Name: "x", Messages: map[string]Message{"screen.panic": {Title: "Crash: {details}"}}},
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
