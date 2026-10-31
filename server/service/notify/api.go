package notify

import (
	"context"
	"errors"
	"slices"
)

// Status is what the Notifications page shows.
type Status struct {
	Language  string        `json:"language"`
	Languages []string      `json:"languages"`
	Channels  []ChannelView `json:"channels"`
	Types     []ChannelType `json:"types"`
	Events    []Kind        `json:"events"`
}

// ChannelUpdate is a channel from the page: an empty secret field keeps the
// stored value, and Clear names the secret fields to empty.
type ChannelUpdate struct {
	Channel
	Clear []string `json:"clear"`
}

// Settings is a save from the page: the whole list of channels.
type Settings struct {
	Language string          `json:"language"`
	Channels []ChannelUpdate `json:"channels"`
}

func (s *Service) Status() Status {
	s.mu.Lock()
	config := s.config
	s.mu.Unlock()
	views := make([]ChannelView, 0, len(config.Channels))
	for _, ch := range config.Channels {
		views = append(views, ch.view())
	}
	return Status{
		Language:  config.Language,
		Languages: Languages,
		Channels:  views,
		Types:     ChannelTypes,
		Events:    Kinds,
	}
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
	next := Config{Language: settings.Language, Channels: []Channel{}}
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
	s.mu.Unlock()
	ch, err := resolve(update, stored)
	if err != nil {
		return err
	}
	return s.Test(ctx, ch)
}
