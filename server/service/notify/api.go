package notify

import (
	"context"
	"errors"
	"slices"
	"time"
)

// Status is what the Notifications page shows.
type Status struct {
	Language  string        `json:"language"`
	Languages []string      `json:"languages"`
	Channels  []ChannelView `json:"channels"`
	Templates []Template    `json:"templates"`
	// Messages are the messages a template can set, with the standard
	// text of the configured language.
	Messages   []MessageView `json:"messages"`
	CommonVars []string      `json:"commonVars"`
	Types      []ChannelType `json:"types"`
	Events     []Kind        `json:"events"`
}

// MessageView is a message spec with its standard text in a language.
type MessageView struct {
	MessageSpec
	Message
}

// ChannelUpdate is a channel from the page: an empty secret field keeps the
// stored value, and Clear names the secret fields to empty.
type ChannelUpdate struct {
	Channel
	Clear []string `json:"clear"`
	// TemplateDraft is the channel's template as it is on the page, for
	// the Test button: it may not be saved yet.
	TemplateDraft *Template `json:"templateDraft,omitempty"`
}

// Settings is a save from the page: the whole list of channels and of
// templates.
type Settings struct {
	Language  string          `json:"language"`
	Channels  []ChannelUpdate `json:"channels"`
	Templates []Template      `json:"templates"`
}

// PreviewRequest asks how a template (nil: the standard one) writes every
// message in a language.
type PreviewRequest struct {
	Language string    `json:"language"`
	Template *Template `json:"template"`
}

// Preview is one message as a channel gets it, with sample values.
type Preview struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Body  string `json:"body"`
	// Text is the body with the device and the time under it, as most
	// channels send it.
	Text string `json:"text"`
}

// messageViews are the messages with their standard text in a language.
func messageViews(language string) []MessageView {
	views := make([]MessageView, 0, len(MessageSpecs))
	for _, spec := range MessageSpecs {
		spec.Vars = append([]string{}, spec.Vars...)
		views = append(views, MessageView{MessageSpec: spec, Message: standardMessage(language, spec.Key)})
	}
	return views
}

func (s *Service) Status() Status {
	s.mu.Lock()
	config := s.config
	s.mu.Unlock()
	views := make([]ChannelView, 0, len(config.Channels))
	for _, ch := range config.Channels {
		views = append(views, ch.view())
	}
	templates := config.Templates
	if templates == nil {
		templates = []Template{}
	}
	return Status{
		Language:   config.Language,
		Languages:  Languages,
		Channels:   views,
		Templates:  templates,
		Messages:   messageViews(config.Language),
		CommonVars: CommonVars,
		Types:      ChannelTypes,
		Events:     Kinds,
	}
}

// previewEvents are the sample events of a preview, one per message.
func previewEvents(now time.Time) []Event {
	samples := map[string]Event{
		"security.lockout":     {Vars: map[string]string{"failures": "5", "address": "203.0.113.7", "minutes": "15"}},
		"security.new-address": {Vars: map[string]string{"user": "admin", "address": "203.0.113.7"}},
		"screen.bsod":          {Details: "Your PC ran into a problem and needs to restart.\nStop code: CRITICAL_PROCESS_DIED"},
		"screen.panic":         {Details: "Kernel panic - not syncing: VFS: Unable to mount root fs on unknown-block(0,0)\nCall Trace:\n dump_stack+0x10"},
	}
	events := make([]Event, 0, len(MessageSpecs))
	for _, spec := range MessageSpecs {
		e := samples[spec.Key]
		e.Kind, e.Message, e.Time = spec.Kind, spec.Key, now
		events = append(events, e)
	}
	return events
}

// Preview writes every message with sample values, as a channel would get
// it from the template (checked first) in the language.
func (s *Service) Preview(request PreviewRequest) ([]Preview, error) {
	language := request.Language
	if !slices.Contains(Languages, language) {
		return nil, errors.New("unknown language")
	}
	if request.Template != nil {
		template := *request.Template
		if template.ID == "" {
			template.ID = "preview"
		}
		if err := template.validate(); err != nil {
			return nil, err
		}
		request.Template = &template
	}
	previews := make([]Preview, 0, len(MessageSpecs))
	for _, e := range previewEvents(s.now()) {
		r := render(e, language, request.Template)
		previews = append(previews, Preview{Key: e.Message, Title: r.Title, Body: r.Body, Text: r.Text()})
	}
	return previews, nil
}

// resolve turns a channel from the page into a stored one: its secrets
// filled in from the stored channel of the same id, the cleared ones
// emptied, defaults applied and the fields checked.
func resolve(update ChannelUpdate, stored []Channel) (Channel, error) {
	ch := withStoredSecrets(update.Channel, stored)
	for _, key := range update.Clear {
		delete(ch.Settings, key)
	}
	if ch.ID == "" {
		ch.ID = newID()
	}
	if err := ch.validate(); err != nil {
		return Channel{}, err
	}
	return ch, nil
}

// Configure stores the page's settings; they apply to the next event.
func (s *Service) Configure(settings Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := Config{Language: settings.Language, Channels: []Channel{}, Templates: settings.Templates}
	if next.Templates == nil {
		next.Templates = []Template{}
	}
	for _, update := range settings.Channels {
		ch, err := resolve(update, s.config.Channels)
		if err != nil {
			name := update.Name
			if name == "" {
				name = update.Type
			}
			return errors.New(name + ": " + err.Error())
		}
		next.Channels = append(next.Channels, ch)
	}
	if err := next.Validate(); err != nil {
		return err
	}
	if err := Save(s.path, next); err != nil {
		return err
	}
	s.config = next
	return nil
}

// TestChannel sends a test message through a channel as it is on the page,
// saved or not.
func (s *Service) TestChannel(ctx context.Context, update ChannelUpdate) error {
	s.mu.Lock()
	stored := slices.Clone(s.config.Channels)
	template := s.config.template(update.Template)
	s.mu.Unlock()
	ch, err := resolve(update, stored)
	if err != nil {
		return err
	}
	if draft := update.TemplateDraft; draft != nil {
		copied := *draft
		if err := copied.validate(); err != nil {
			return err
		}
		template = &copied
	}
	return s.Test(ctx, ch, template)
}
