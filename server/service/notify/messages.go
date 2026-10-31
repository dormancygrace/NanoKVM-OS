package notify

import (
	"embed"
	"encoding/json"
	"path"
	"regexp"
	"strings"
	"time"
)

// Languages are the languages of the standard messages, those of the web
// interface; the first is the default.
var Languages = []string{"en", "ca", "cz", "da", "de", "es", "fr", "he", "hu", "id", "it", "ja", "ko", "nb", "nl",
	"pl", "pt_br", "ru", "se", "th", "tr", "uk", "vi", "zh", "zh_tw"}

// Message is the title and body of one event, with {name} variables.
type Message struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// MessageSpec is one message an event can send: its key, the kind of event
// it is about and the variables of its own.
type MessageSpec struct {
	Key  string   `json:"key"`
	Kind Kind     `json:"kind"`
	Vars []string `json:"vars"`
}

// MessageSpecs are the messages, in the page's order.
var MessageSpecs = []MessageSpec{
	{Key: "power.on", Kind: KindPower},
	{Key: "power.off", Kind: KindPower},
	{Key: "hdmi.lost", Kind: KindHDMI},
	{Key: "hdmi.restored", Kind: KindHDMI},
	{Key: "security.lockout", Kind: KindSecurity, Vars: []string{"failures", "address", "minutes"}},
	{Key: "security.new-address", Kind: KindSecurity, Vars: []string{"user", "address"}},
	{Key: "screen.bsod", Kind: KindScreen},
	{Key: "screen.panic", Kind: KindScreen},
	{Key: "test", Kind: KindTest},
}

// CommonVars are the variables of every message: the device's name, the
// event's time, and the details sent with it (the text read off the screen).
// Details the body does not place go under it.
var CommonVars = []string{"device", "time", "details"}

func messageSpec(key string) (MessageSpec, bool) {
	for _, spec := range MessageSpecs {
		if spec.Key == key {
			return spec, true
		}
	}
	return MessageSpec{}, false
}

//go:embed messages/*.json
var messageFiles embed.FS

// messages holds the standard messages of every language.
var messages = func() map[string]map[string]Message {
	all := map[string]map[string]Message{}
	for _, language := range Languages {
		data, err := messageFiles.ReadFile(path.Join("messages", language+".json"))
		if err != nil {
			panic(err)
		}
		catalogue := map[string]Message{}
		if err := json.Unmarshal(data, &catalogue); err != nil {
			panic(language + ": " + err.Error())
		}
		all[language] = catalogue
	}
	return all
}()

// standardMessage is a message of the standard template, falling back to
// English.
func standardMessage(language, key string) Message {
	if m, ok := messages[language][key]; ok {
		return m
	}
	if m, ok := messages[Languages[0]][key]; ok {
		return m
	}
	return Message{Title: key}
}

var variable = regexp.MustCompile(`\{([a-z]+)\}`)

// fill replaces each {name} of vars; other braces stay as written.
func fill(text string, vars map[string]string) string {
	return variable.ReplaceAllStringFunc(text, func(match string) string {
		if value, ok := vars[match[1:len(match)-1]]; ok {
			return value
		}
		return match
	})
}

// unknownVars lists the {name}s of text that the message does not have.
func unknownVars(text string, spec MessageSpec) []string {
	var unknown []string
	for _, match := range variable.FindAllStringSubmatch(text, -1) {
		name := match[1]
		if !contains(spec.Vars, name) && !contains(CommonVars, name) && !contains(unknown, name) {
			unknown = append(unknown, name)
		}
	}
	return unknown
}

func contains(list []string, item string) bool {
	for _, value := range list {
		if value == item {
			return true
		}
	}
	return false
}

// render puts an event into words: the template's text where it has one,
// the standard message of the language elsewhere.
func render(e Event, language string, template *Template) Rendered {
	m := standardMessage(language, e.Message)
	if template != nil {
		if own, ok := template.Messages[e.Message]; ok {
			if strings.TrimSpace(own.Title) != "" {
				m.Title = own.Title
			}
			if strings.TrimSpace(own.Body) != "" {
				m.Body = own.Body
			}
		}
	}
	vars := map[string]string{
		"device":  deviceName(),
		"time":    e.Time.Format(time.DateTime + " MST"),
		"details": e.Details,
	}
	for name, value := range e.Vars {
		vars[name] = value
	}
	body := fill(m.Body, vars)
	if e.Details != "" && !strings.Contains(m.Body, "{details}") {
		if body != "" {
			body += "\n\n"
		}
		body += e.Details
	}
	return Rendered{
		Kind:     e.Kind,
		Severity: e.Severity,
		Title:    fill(m.Title, vars),
		Body:     body,
		Device:   vars["device"],
		Time:     e.Time,
		Image:    e.Image,
	}
}
