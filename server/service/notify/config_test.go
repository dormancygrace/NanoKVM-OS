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

func TestValidateDefaultsAndDropsUnknownFields(t *testing.T) {
	ch := Channel{Type: "email", Settings: map[string]string{
		"host": " smtp.example.com ", "from": "kvm@example.com", "to": "a@example.com, b@example.com",
		"token": "from another type",
	}}
	if err := ch.validate(); err != nil {
		t.Fatal(err)
	}
	if ch.Settings["port"] != "587" || ch.Settings["security"] != "starttls" {
		t.Fatalf("defaults not applied: %v", ch.Settings)
	}
	if ch.Settings["host"] != "smtp.example.com" {
		t.Fatalf("value not trimmed: %q", ch.Settings["host"])
	}
	if _, ok := ch.Settings["token"]; ok {
		t.Fatal("a field of another type was kept")
	}
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

func TestConfigureRejectsWithoutSaving(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s := newService(path)
	bad := ChannelUpdate{Channel: Channel{Type: "slack"}}
	if err := s.Configure(Settings{Language: "en", Channels: []ChannelUpdate{bad}}); err == nil {
		t.Fatal("accepted a channel without its URL")
	}
	if err := s.Configure(Settings{Language: "xx"}); err == nil {
		t.Fatal("accepted an unknown language")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("a refused save wrote the file")
	}
}

func TestRememberAddress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "addresses.json")
	check := func(user, address string, want bool) {
		t.Helper()
		known, err := rememberAddress(path, user, address)
		if err != nil || known != want {
			t.Fatalf("%s from %s: known %v, %v; want %v", user, address, known, err, want)
		}
	}
	check("admin", "10.0.0.1", true) // the first login only starts the list
	check("admin", "10.0.0.1", true)
	check("admin", "10.0.0.2", false)
	check("admin", "10.0.0.2", true)
	check("guest", "10.0.0.2", true)
	for i := 0; i < maxAddresses; i++ {
		check("admin", "192.0.2."+string(rune('a'+i%26))+string(rune('a'+i/26)), false)
	}
	check("admin", "10.0.0.1", false) // forgotten: the list holds the newest
}
