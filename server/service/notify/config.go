package notify

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"NanoKVM-Server/internal/atomicfile"
)

// ConfigPath is root-only: it holds bot tokens, passwords and webhook URLs.
const ConfigPath = "/etc/kvm/notify/config.json"

const (
	maxChannels   = 16
	maxValueBytes = 2048
	maxTemplates  = 16
	maxTitleBytes = 256
	maxBodyBytes  = 2048
)

type Config struct {
	Language  string     `json:"language"`
	Channels  []Channel  `json:"channels"`
	Templates []Template `json:"templates"`
}

// Template is a named set of messages; a channel that picks it gets its
// text for each message it has, the standard message elsewhere (an empty
// title or body is the standard one too).
type Template struct {
	// ID is chosen by the page, so a channel can pick a template not saved
	// yet: up to 32 lower case letters and digits.
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Messages map[string]Message `json:"messages"`
}

var templateID = regexp.MustCompile(`^[a-z0-9]{1,32}$`)

// template is the template of an id, nil for the standard one or an id
// not stored.
func (c Config) template(id string) *Template {
	for i := range c.Templates {
		if c.Templates[i].ID == id {
			return &c.Templates[i]
		}
	}
	return nil
}

func (t *Template) validate() error {
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" || len(t.Name) > 64 {
		return errors.New("a template needs a name of at most 64 characters")
	}
	if !templateID.MatchString(t.ID) {
		return fmt.Errorf("%s: invalid id", t.Name)
	}
	kept := map[string]Message{}
	for key, m := range t.Messages {
		spec, ok := messageSpec(key)
		if !ok {
			return fmt.Errorf("%s: unknown message %q", t.Name, key)
		}
		m.Title, m.Body = strings.TrimSpace(m.Title), strings.TrimSpace(m.Body)
		if m.Title == "" && m.Body == "" {
			continue // the standard message
		}
		switch {
		case len(m.Title) > maxTitleBytes || strings.ContainsAny(m.Title, "\r\n"):
			return fmt.Errorf("%s: %s: the title is one line of at most %d bytes", t.Name, key, maxTitleBytes)
		case len(m.Body) > maxBodyBytes:
			return fmt.Errorf("%s: %s: the text is longer than %d bytes", t.Name, key, maxBodyBytes)
		case strings.Contains(m.Title, "{details}"):
			// The details are lines read off the screen; a title is a mail
			// subject or a syslog header.
			return fmt.Errorf("%s: %s: {details} goes in the text, not the title", t.Name, key)
		}
		if unknown := unknownVars(m.Title+" "+m.Body, spec); len(unknown) > 0 {
			return fmt.Errorf("%s: %s: unknown variable {%s}", t.Name, key, strings.Join(unknown, "}, {"))
		}
		kept[key] = m
	}
	t.Messages = kept
	return nil
}

// Channel is one destination. Settings holds the fields of its type
// (channelTypes); secret fields never leave the device.
type Channel struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// Events are the kinds the channel gets.
	Events   []Kind            `json:"events"`
	Settings map[string]string `json:"settings"`
	// Template is the id of the channel's template; "" is the standard one.
	Template string `json:"template,omitempty"`
}

func (ch Channel) wants(kind Kind) bool {
	return kind == KindTest || slices.Contains(ch.Events, kind)
}

func (ch Channel) get(key string) string { return strings.TrimSpace(ch.Settings[key]) }

func (ch Channel) flag(key string) bool { return ch.get(key) == "true" }

// Field is one setting of a channel type, as the page draws it. The page
// translates its label by Key.
type Field struct {
	Key string `json:"key"`
	// Kind: text, url, email, number, select or bool.
	Kind     string   `json:"kind"`
	Secret   bool     `json:"secret,omitempty"`
	Required bool     `json:"required,omitempty"`
	Options  []string `json:"options,omitempty"`
	Default  string   `json:"default,omitempty"`
	// Placeholder is a literal example, not translated.
	Placeholder string `json:"placeholder,omitempty"`
}

type ChannelType struct {
	Type   string  `json:"type"`
	Fields []Field `json:"fields"`
	// Image: the channel can carry a screenshot.
	Image bool `json:"image"`
}

var skipVerify = Field{Key: "skipVerify", Kind: "bool"}

// ChannelTypes are the kinds of channel, in the page's order.
var ChannelTypes = []ChannelType{
	{Type: "telegram", Image: true, Fields: []Field{
		{Key: "token", Kind: "text", Secret: true, Required: true, Placeholder: "123456:ABC-DEF…"},
		{Key: "chatId", Kind: "text", Required: true, Placeholder: "-1001234567890"},
		{Key: "threadId", Kind: "number"},
		{Key: "apiUrl", Kind: "url", Placeholder: "https://api.telegram.org"},
	}},
	{Type: "email", Image: true, Fields: []Field{
		{Key: "host", Kind: "text", Required: true, Placeholder: "smtp.example.com"},
		{Key: "port", Kind: "number", Default: "587"},
		{Key: "security", Kind: "select", Options: []string{"starttls", "tls", "none"}, Default: "starttls"},
		{Key: "username", Kind: "text"},
		{Key: "password", Kind: "text", Secret: true},
		{Key: "from", Kind: "email", Required: true, Placeholder: "nanokvm@example.com"},
		{Key: "to", Kind: "text", Required: true, Placeholder: "admin@example.com, ops@example.com"},
		skipVerify,
	}},
	{Type: "webhook", Image: true, Fields: []Field{
		{Key: "url", Kind: "url", Required: true, Placeholder: "https://example.com/hooks/nanokvm"},
		{Key: "hmacSecret", Kind: "text", Secret: true},
		{Key: "authorization", Kind: "text", Secret: true, Placeholder: "Bearer …"},
		skipVerify,
	}},
	{Type: "slack", Fields: []Field{
		{Key: "url", Kind: "url", Secret: true, Required: true, Placeholder: "https://hooks.slack.com/services/…"},
		skipVerify,
	}},
	{Type: "teams", Fields: []Field{
		{Key: "url", Kind: "url", Secret: true, Required: true, Placeholder: "https://….powerplatform.com/…/invoke?…"},
	}},
	{Type: "googlechat", Fields: []Field{
		{Key: "url", Kind: "url", Secret: true, Required: true, Placeholder: "https://chat.googleapis.com/v1/spaces/…"},
	}},
	{Type: "discord", Image: true, Fields: []Field{
		{Key: "url", Kind: "url", Secret: true, Required: true, Placeholder: "https://discord.com/api/webhooks/…"},
	}},
	{Type: "ntfy", Fields: []Field{
		{Key: "server", Kind: "url", Default: "https://ntfy.sh"},
		{Key: "topic", Kind: "text", Secret: true, Required: true},
		{Key: "token", Kind: "text", Secret: true, Placeholder: "tk_…"},
		skipVerify,
	}},
	{Type: "gotify", Fields: []Field{
		{Key: "server", Kind: "url", Required: true, Placeholder: "https://gotify.example.com"},
		{Key: "token", Kind: "text", Secret: true, Required: true},
		skipVerify,
	}},
	{Type: "pushover", Image: true, Fields: []Field{
		{Key: "appToken", Kind: "text", Secret: true, Required: true},
		{Key: "userKey", Kind: "text", Secret: true, Required: true},
		{Key: "device", Kind: "text"},
	}},
	{Type: "matrix", Fields: []Field{
		{Key: "homeserver", Kind: "url", Required: true, Placeholder: "https://matrix.example.com"},
		{Key: "accessToken", Kind: "text", Secret: true, Required: true},
		{Key: "roomId", Kind: "text", Required: true, Placeholder: "!abcdef:example.com"},
		skipVerify,
	}},
	{Type: "syslog", Fields: []Field{
		{Key: "host", Kind: "text", Required: true, Placeholder: "siem.example.com"},
		{Key: "port", Kind: "number", Default: "514"},
		{Key: "protocol", Kind: "select", Options: []string{"udp", "tcp", "tls"}, Default: "udp"},
		skipVerify,
	}},
}

func channelType(name string) (ChannelType, bool) {
	for _, t := range ChannelTypes {
		if t.Type == name {
			return t, true
		}
	}
	return ChannelType{}, false
}

func defaultConfig() Config { return Config{Language: Languages[0]} }

func Load(path string) (Config, error) {
	c := defaultConfig()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return defaultConfig(), fmt.Errorf("read %s: %w", path, err)
	}
	if !slices.Contains(Languages, c.Language) {
		c.Language = Languages[0]
	}
	return c, nil
}

func Save(path string, c Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return atomicfile.Write(path, data, 0o600)
}

// Validate checks the settings and fills in each field's default.
func (c *Config) Validate() error {
	if !slices.Contains(Languages, c.Language) {
		return fmt.Errorf("unknown language %q", c.Language)
	}
	if len(c.Channels) > maxChannels {
		return fmt.Errorf("at most %d channels", maxChannels)
	}
	if len(c.Templates) > maxTemplates {
		return fmt.Errorf("at most %d templates", maxTemplates)
	}
	templates, names := map[string]bool{}, map[string]bool{}
	for i := range c.Templates {
		if err := c.Templates[i].validate(); err != nil {
			return err
		}
		// A channel picks a template by its name on the page.
		name := strings.ToLower(c.Templates[i].Name)
		if templates[c.Templates[i].ID] || names[name] {
			return fmt.Errorf("template %q appears twice", c.Templates[i].Name)
		}
		templates[c.Templates[i].ID], names[name] = true, true
	}
	for _, ch := range c.Channels {
		if ch.Template != "" && !templates[ch.Template] {
			return fmt.Errorf("%s: the channel's template does not exist", ch.Name)
		}
	}
	ids := map[string]bool{}
	for i := range c.Channels {
		ch := &c.Channels[i]
		if ids[ch.ID] {
			return fmt.Errorf("channel %q appears twice", ch.ID)
		}
		ids[ch.ID] = true
		if err := ch.validate(); err != nil {
			name := ch.Name
			if name == "" {
				name = ch.Type
			}
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

func (ch *Channel) validate() error {
	t, ok := channelType(ch.Type)
	if !ok {
		return fmt.Errorf("unknown channel type %q", ch.Type)
	}
	ch.Name = strings.TrimSpace(ch.Name)
	if len(ch.Name) > 64 {
		return errors.New("the name is longer than 64 characters")
	}
	for _, kind := range ch.Events {
		if !slices.Contains(Kinds, kind) {
			return fmt.Errorf("unknown event %q", kind)
		}
	}
	settings := map[string]string{}
	for _, f := range t.Fields {
		value := strings.TrimSpace(ch.Settings[f.Key])
		if value == "" {
			value = f.Default
		}
		if value == "" {
			if f.Required {
				return fmt.Errorf("%s is required", f.Key)
			}
			continue
		}
		if len(value) > maxValueBytes {
			return fmt.Errorf("%s is too long", f.Key)
		}
		if err := checkValue(f, value); err != nil {
			return fmt.Errorf("%s: %w", f.Key, err)
		}
		settings[f.Key] = value
	}
	// Fields of no field list (another type's, a typo) are dropped.
	ch.Settings = settings
	if ch.Type == "email" {
		for _, to := range splitList(settings["to"]) {
			if _, err := mail.ParseAddress(to); err != nil {
				return fmt.Errorf("to: %q is not an address", to)
			}
		}
	}
	return nil
}

func checkValue(f Field, value string) error {
	switch f.Kind {
	case "url":
		u, err := url.Parse(value)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("not an http or https address")
		}
	case "email":
		if _, err := mail.ParseAddress(value); err != nil {
			return errors.New("not an e-mail address")
		}
	case "number":
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < 0 || (f.Key == "port" && (n < 1 || n > 65535)) {
			return errors.New("not a valid number")
		}
	case "select":
		if !slices.Contains(f.Options, value) {
			return errors.New("not one of the choices")
		}
	case "bool":
		if value != "true" && value != "false" {
			return errors.New("not true or false")
		}
	}
	if strings.ContainsAny(value, "\r\n") {
		return errors.New("must be one line")
	}
	return nil
}

func splitList(value string) []string {
	var out []string
	for _, item := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

// ChannelView is a channel as the page sees it: secret fields are left out
// and SecretsSet names the ones stored.
type ChannelView struct {
	Channel
	SecretsSet []string `json:"secretsSet"`
}

func (ch Channel) view() ChannelView {
	t, _ := channelType(ch.Type)
	v := ChannelView{Channel: ch, SecretsSet: []string{}}
	v.Settings = map[string]string{}
	v.Events = slices.Clone(ch.Events)
	if v.Events == nil {
		v.Events = []Kind{}
	}
	for key, value := range ch.Settings {
		v.Settings[key] = value
	}
	for _, f := range t.Fields {
		if f.Secret {
			if v.Settings[f.Key] != "" {
				v.SecretsSet = append(v.SecretsSet, f.Key)
			}
			delete(v.Settings, f.Key)
		}
	}
	return v
}

// withStoredSecrets fills the secret fields left empty with the stored
// values of the same channel, so the page can save without knowing them.
func withStoredSecrets(ch Channel, stored []Channel) Channel {
	t, _ := channelType(ch.Type)
	settings := map[string]string{}
	for key, value := range ch.Settings {
		settings[key] = value
	}
	for _, old := range stored {
		if old.ID != ch.ID || old.Type != ch.Type || ch.ID == "" {
			continue
		}
		for _, f := range t.Fields {
			if f.Secret && strings.TrimSpace(settings[f.Key]) == "" {
				settings[f.Key] = old.Settings[f.Key]
			}
		}
	}
	ch.Settings = settings
	return ch
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
