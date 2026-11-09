package notify

import (
	"encoding/json"
	"os"
	"path/filepath"

	"strings"
	"testing"
)

func telegramUpdate(id, token string) ChannelUpdate {
	return ChannelUpdate{Channel: Channel{
		ID: id, Type: "telegram", Name: "ops", Enabled: true,
		Events:   []Kind{KindPower},
		Settings: map[string]string{"token": token, "chatId": "-100"},
	}}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]Channel{
		"missing required": {Type: "telegram", Settings: map[string]string{"token": "x"}},
		"unknown type":     {Type: "fax"},
		"bad url":          {Type: "slack", Settings: map[string]string{"url": "ftp://example.com"}},
		"bad port":         {Type: "syslog", Settings: map[string]string{"host": "h", "port": "70000"}},
		"bad choice":       {Type: "syslog", Settings: map[string]string{"host": "h", "protocol": "sctp"}},
		"bad recipient":    {Type: "email", Settings: map[string]string{"host": "h", "from": "a@b.c", "to": "nobody"}},
		"header injection": {Type: "email", Settings: map[string]string{"host": "h", "from": "a@b.c\r\nBcc: x@y.z", "to": "a@b.c"}},
		"unknown event":    {Type: "slack", Events: []Kind{"weather"}, Settings: map[string]string{"url": "https://h/x"}},
	}
	for name, ch := range cases {
		if err := ch.validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestSecretsStayOnTheDevice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notify", "config.json")
	s := newService(path)
	if err := s.Configure(Settings{Language: "en", Channels: []ChannelUpdate{telegramUpdate("", "123:SECRET")}}); err != nil {
		t.Fatal(err)
	}
	status := s.Status()
	data, _ := json.Marshal(status)
	if strings.Contains(string(data), "SECRET") {
		t.Fatalf("the status carries the token: %s", data)
	}
	if len(status.Channels) != 1 || status.Channels[0].ID == "" || status.Channels[0].SecretsSet[0] != "token" {
		t.Fatalf("status: %+v", status.Channels)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config file mode: %v %v", info, err)
	}

	// Saved again without the token, as the page does: the token stays.
	id := status.Channels[0].ID
	update := telegramUpdate(id, "")
	update.Name = "renamed"
	if err := s.Configure(Settings{Language: "ru", Channels: []ChannelUpdate{update}}); err != nil {
		t.Fatal(err)
	}
	stored, _ := Load(path)
	if stored.Channels[0].Settings["token"] != "123:SECRET" || stored.Channels[0].Name != "renamed" || stored.Language != "ru" {
		t.Fatalf("stored: %+v", stored)
	}

	// Another id does not inherit it, so the save is refused.
	if err := s.Configure(Settings{Language: "en", Channels: []ChannelUpdate{telegramUpdate("other", "")}}); err == nil {
		t.Fatal("a channel took another channel's token")
	}

	// Clear empties an optional secret.
	hook := ChannelUpdate{Channel: Channel{ID: "h", Type: "webhook", Settings: map[string]string{
		"url": "https://example.com/x", "hmacSecret": "k"}}}
	if err := s.Configure(Settings{Language: "en", Channels: []ChannelUpdate{hook}}); err != nil {
		t.Fatal(err)
	}
	hook.Settings = map[string]string{"url": "https://example.com/x"}
	hook.Clear = []string{"hmacSecret"}
	if err := s.Configure(Settings{Language: "en", Channels: []ChannelUpdate{hook}}); err != nil {
		t.Fatal(err)
	}
	stored, _ = Load(path)
	if _, ok := stored.Channels[0].Settings["hmacSecret"]; ok {
		t.Fatal("cleared secret kept")
	}
}

// the first login only starts the list

// forgotten: the list holds the newest
// An address in use stays while newer ones push the others out.
