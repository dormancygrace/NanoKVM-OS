// Package notify sends the device's events (host power, HDMI signal, login
// security, a crash on the host's screen) to the channels set on the
// Notifications page: Telegram, e-mail, chat webhooks, push services and
// syslog. Producers call Emit; it never blocks them.
package notify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// Kind is what an event is about; each channel picks the kinds it gets.
type Kind string

const (
	KindPower    Kind = "power"
	KindHDMI     Kind = "hdmi"
	KindSecurity Kind = "security"
	// KindScreen: a crash seen on the host's screen (the TPU add-on).
	KindScreen Kind = "screen"
	// KindTest is the page's Test button; it ignores the channel's kinds.
	KindTest Kind = "test"
)

// Kinds are the kinds a channel can choose, in the page's order.
var Kinds = []Kind{KindPower, KindHDMI, KindSecurity, KindScreen}

type Severity int

const (
	SeverityInfo Severity = iota
	SeverityWarning
	SeverityCritical
)

func (s Severity) String() string {
	switch s {
	case SeverityWarning:
		return "warning"
	case SeverityCritical:
		return "critical"
	}
	return "info"
}

// Event is one notification. Message is a key of the message catalogue
// (messages.go), filled with Args in the configured language.
type Event struct {
	Kind     Kind
	Severity Severity
	Message  string
	Args     []any
	// Details is sent as it is, untranslated: for example the text read
	// off the host's screen.
	Details string
	// Image is an optional JPEG, sent by the channels that can carry one.
	Image []byte
	Time  time.Time
}

// Rendered is an event in words, ready for a channel.
type Rendered struct {
	Kind     Kind
	Severity Severity
	Title    string
	Body     string
	Device   string
	Time     time.Time
	Image    []byte
}

// Text is the body with the device and the time under it, for channels with
// no title or fields of their own.
func (r Rendered) Text() string {
	return r.Body + "\n\n" + r.Device + " · " + r.Time.Format("2006-01-02 15:04:05 MST")
}

const (
	queueSize = 32
	// An event with the same message and arguments within this time is
	// dropped: a producer that repeats itself must not flood a chat.
	repeatWindow = time.Minute
)

// retryDelays are the waits before the second, third and fourth attempts.
var retryDelays = []time.Duration{10 * time.Second, time.Minute, 5 * time.Minute}

// permanentError is a failure that a retry cannot fix (a wrong token, a
// refused address): the channel's settings must change first.
type permanentError struct{ err error }

func (e permanentError) Error() string { return e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }

func permanent(err error) error { return permanentError{err} }

type Service struct {
	path  string
	queue chan Event
	send  func(ctx context.Context, ch Channel, r Rendered) error
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) bool

	mu     sync.Mutex
	config Config
	recent map[string]time.Time

	startOnce sync.Once
}

func newService(path string) *Service {
	s := &Service{
		path:   path,
		queue:  make(chan Event, queueSize),
		send:   sendChannel,
		now:    time.Now,
		sleep:  sleepContext,
		recent: make(map[string]time.Time),
	}
	config, err := Load(path)
	if err != nil {
		log.Errorf("notifications: %v", err)
	}
	s.config = config
	return s
}

var (
	std     *Service
	stdOnce sync.Once
)

// Default is the device's notification service, with its stored settings.
func Default() *Service {
	stdOnce.Do(func() { std = newService(ConfigPath) })
	return std
}

// Emit queues an event for the device's channels.
func Emit(e Event) { Default().Emit(e) }

// Emit queues an event. A full queue drops it: producers are never delayed.
func (s *Service) Emit(e Event) {
	s.startOnce.Do(func() { go s.run() })
	if e.Time.IsZero() {
		e.Time = s.now()
	}
	select {
	case s.queue <- e:
	default:
		log.Warnf("notifications: queue full, dropped %s", e.Message)
	}
}

func (s *Service) run() {
	for e := range s.queue {
		s.dispatch(e)
	}
}

// dispatch hands the event to every enabled channel that takes its kind;
// each channel retries on its own, so a slow one does not hold the others.
func (s *Service) dispatch(e Event) {
	s.mu.Lock()
	config := s.config
	key := fmt.Sprint(e.Message, e.Args)
	now := s.now()
	for k, at := range s.recent {
		if now.Sub(at) >= repeatWindow {
			delete(s.recent, k)
		}
	}
	_, repeated := s.recent[key]
	if !repeated {
		s.recent[key] = now
	}
	s.mu.Unlock()
	if repeated {
		log.Debugf("notifications: repeated %s dropped", e.Message)
		return
	}
	var targets []Channel
	for _, ch := range config.Channels {
		if ch.Enabled && ch.wants(e.Kind) {
			targets = append(targets, ch)
		}
	}
	if len(targets) == 0 {
		return
	}
	r := render(e, config.Language)
	for _, ch := range targets {
		go s.deliver(ch, r)
	}
}

func (s *Service) deliver(ch Channel, r Rendered) {
	for attempt := 0; ; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
		err := s.send(ctx, ch, r)
		cancel()
		if err == nil {
			return
		}
		var perm permanentError
		if errors.As(err, &perm) || attempt >= len(retryDelays) {
			log.Warnf("notifications: %s %q: %v", ch.Type, ch.Name, err)
			return
		}
		log.Debugf("notifications: %s %q: %v; retrying", ch.Type, ch.Name, err)
		if !s.sleep(context.Background(), retryDelays[attempt]) {
			return
		}
	}
}

// Test sends a test message to one channel at once and reports the result.
func (s *Service) Test(ctx context.Context, ch Channel) error {
	s.mu.Lock()
	language := s.config.Language
	s.mu.Unlock()
	r := render(Event{Kind: KindTest, Message: "test", Time: s.now()}, language)
	return s.send(ctx, ch, r)
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// deviceName is the host name the messages are signed with.
func deviceName() string {
	name, err := os.Hostname()
	if err != nil || strings.TrimSpace(name) == "" {
		return "NanoKVM"
	}
	return name
}
