// Package addons reads the manifests installed add-ons put in
// /usr/share/nanokvm/addons.d: what the web UI shows for them (a settings
// page described as a form) and where their own API listens (a unix socket
// the server proxies to). docs/ADDONS.md is the contract.
package addons

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ManifestDir holds one <id>.json per installed add-on, files of its package.
const ManifestDir = "/usr/share/nanokvm/addons.d"

// SocketDir holds the add-ons' API sockets.
const SocketDir = "/run/nanokvm/addons"

// APIVersion is the manifest format this server reads. A manifest asking for
// another one is skipped: its pages may need what this server lacks.
const APIVersion = 1

// manifestLimit bounds one manifest file.
const manifestLimit = 64 << 10

var (
	idPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	socketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}\.sock$`)
	keyPattern    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
	iconPattern   = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
)

// The settings groups an add-on page can join (web settings/tabs.ts).
var settingsGroups = map[string]bool{
	"usb": true, "network": true, "services": true, "system": true, "ai": true, "software": true,
}

// The core settings pages an add-on can add a section to: those whose
// subject the add-on extends (the web UI draws the sections there).
var settingsSections = map[string]bool{"services-bmc": true}

// Text is a string in several languages, keyed by the web UI's language tags
// ("en", "ru", "zh", ...). English is required: it is the fallback.
type Text map[string]string

// Manifest is one add-on's addons.d/<id>.json.
type Manifest struct {
	ID          string    `json:"id"`
	API         int       `json:"api"`
	Title       Text      `json:"title"`
	Description Text      `json:"description,omitempty"`
	Backend     *Backend  `json:"backend,omitempty"`
	Settings    *Settings `json:"settings,omitempty"`
}

// Backend is the add-on's own HTTP API: a unix socket in SocketDir, reached
// by administrators at /api/addon/<id>/... through the server.
type Backend struct {
	Socket string `json:"socket"`
}

// Settings is where the add-on's form appears: a page of its own at the
// end of Group, or a section at the end of the core page Section.
type Settings struct {
	Group   string `json:"group,omitempty"`
	Section string `json:"section,omitempty"`
	// Icon of its own page: a lucide icon name the web UI knows; others
	// show a generic one.
	Icon string `json:"icon,omitempty"`
	Form Form   `json:"form"`
}

// Form is a settings page drawn by the web UI from this description.
type Form struct {
	// Source answers GET with {"code":0,"data":{...}}: the fields' values.
	Source string `json:"source"`
	// Submit, when set, takes a POST of the edited values (no status
	// fields, no empty password) and answers {"code":0}.
	Submit string `json:"submit,omitempty"`
	// Refresh reloads Source every so many seconds while the page is open.
	Refresh int      `json:"refresh,omitempty"`
	Fields  []Field  `json:"fields"`
	Actions []Action `json:"actions,omitempty"`
}

// Field kinds.
const (
	FieldText     = "text"
	FieldPassword = "password"
	FieldNumber   = "number"
	FieldSelect   = "select"
	FieldSwitch   = "switch"
	FieldStatus   = "status"
)

var fieldKinds = map[string]bool{
	FieldText: true, FieldPassword: true, FieldNumber: true,
	FieldSelect: true, FieldSwitch: true, FieldStatus: true,
}

// Field is one value of the form, data[key] of Source.
type Field struct {
	Key   string `json:"key"`
	Type  string `json:"type"`
	Label Text   `json:"label"`
	Help  Text   `json:"help,omitempty"`
	// Options of a select.
	Options []Option `json:"options,omitempty"`
	// Min and Max bound a number.
	Min      *float64 `json:"min,omitempty"`
	Max      *float64 `json:"max,omitempty"`
	Required bool     `json:"required,omitempty"`
}

// Option is one choice of a select.
type Option struct {
	Value string `json:"value"`
	Label Text   `json:"label"`
}

// Action is a button that POSTs to URL with no body.
type Action struct {
	ID      string `json:"id"`
	Label   Text   `json:"label"`
	URL     string `json:"url"`
	Confirm Text   `json:"confirm,omitempty"`
	Danger  bool   `json:"danger,omitempty"`
}

// SocketPath is where the add-on's API listens.
func (m *Manifest) SocketPath() string {
	if m.Backend == nil {
		return ""
	}
	return filepath.Join(SocketDir, m.Backend.Socket)
}

// Validate checks a manifest read from file. Its URLs must stay inside the
// add-on's own API: a manifest cannot point the web UI at the server's.
func (m *Manifest) Validate() error {
	if !idPattern.MatchString(m.ID) {
		return fmt.Errorf("bad id %q", m.ID)
	}
	if m.API != APIVersion {
		return fmt.Errorf("api %d, this server reads %d", m.API, APIVersion)
	}
	if err := m.Title.validate("title", true); err != nil {
		return err
	}
	if err := m.Description.validate("description", false); err != nil {
		return err
	}
	if m.Backend != nil && !socketPattern.MatchString(m.Backend.Socket) {
		return fmt.Errorf("bad socket %q: a file name ending in .sock", m.Backend.Socket)
	}
	if m.Settings == nil {
		return nil
	}
	s := m.Settings
	switch {
	case s.Group != "" && s.Section != "":
		return errors.New("settings: a group (own page) or a section (core page), not both")
	case s.Section != "":
		if !settingsSections[s.Section] {
			return fmt.Errorf("unknown settings section %q", s.Section)
		}
		if s.Icon != "" {
			return errors.New("settings: an icon belongs to a page of its own")
		}
	case !settingsGroups[s.Group]:
		return fmt.Errorf("unknown settings group %q", s.Group)
	}
	if s.Icon != "" && !iconPattern.MatchString(s.Icon) {
		return fmt.Errorf("bad icon %q", s.Icon)
	}
	if m.Backend == nil {
		return errors.New("a settings form needs a backend")
	}
	prefix := "/api/addon/" + m.ID + "/"
	url := func(what, value string) error {
		if !strings.HasPrefix(value, prefix) || strings.Contains(value, "..") ||
			strings.ContainsAny(value, "?#\\ ") {
			return fmt.Errorf("%s %q: must be a path under %s", what, value, prefix)
		}
		return nil
	}
	f := s.Form
	if err := url("form source", f.Source); err != nil {
		return err
	}
	if f.Submit != "" {
		if err := url("form submit", f.Submit); err != nil {
			return err
		}
	}
	if f.Refresh < 0 || f.Refresh > 3600 || (f.Refresh > 0 && f.Refresh < 2) {
		return fmt.Errorf("refresh %d: 0 or 2..3600 seconds", f.Refresh)
	}
	if len(f.Fields) == 0 || len(f.Fields) > 64 {
		return errors.New("a form has 1 to 64 fields")
	}
	keys := map[string]bool{}
	for _, field := range f.Fields {
		if !keyPattern.MatchString(field.Key) || keys[field.Key] {
			return fmt.Errorf("bad or repeated field key %q", field.Key)
		}
		keys[field.Key] = true
		if !fieldKinds[field.Type] {
			return fmt.Errorf("field %s: unknown type %q", field.Key, field.Type)
		}
		if err := field.Label.validate("field "+field.Key+" label", true); err != nil {
			return err
		}
		if err := field.Help.validate("field "+field.Key+" help", false); err != nil {
			return err
		}
		if (field.Type == FieldSelect) != (len(field.Options) > 0) {
			return fmt.Errorf("field %s: options belong to a select, and a select needs them", field.Key)
		}
		for _, option := range field.Options {
			if err := option.Label.validate("field "+field.Key+" option label", true); err != nil {
				return err
			}
		}
		if (field.Min != nil || field.Max != nil) && field.Type != FieldNumber {
			return fmt.Errorf("field %s: min and max belong to a number", field.Key)
		}
		if field.Min != nil && field.Max != nil && *field.Min > *field.Max {
			return fmt.Errorf("field %s: min above max", field.Key)
		}
	}
	editable := false
	for _, field := range f.Fields {
		editable = editable || field.Type != FieldStatus
	}
	if editable != (f.Submit != "") {
		return errors.New("a form with editable fields needs submit, and only such a form has one")
	}
	ids := map[string]bool{}
	for _, action := range f.Actions {
		if !keyPattern.MatchString(action.ID) || ids[action.ID] {
			return fmt.Errorf("bad or repeated action id %q", action.ID)
		}
		ids[action.ID] = true
		if err := action.Label.validate("action "+action.ID+" label", true); err != nil {
			return err
		}
		if err := action.Confirm.validate("action "+action.ID+" confirm", false); err != nil {
			return err
		}
		if err := url("action "+action.ID, action.URL); err != nil {
			return err
		}
	}
	return nil
}

func (t Text) validate(what string, required bool) error {
	if len(t) == 0 {
		if required {
			return fmt.Errorf("%s: missing", what)
		}
		return nil
	}
	if strings.TrimSpace(t["en"]) == "" {
		return fmt.Errorf("%s: an English text is required", what)
	}
	for lang, text := range t {
		if len(lang) < 2 || len(lang) > 8 || len(text) > 1024 {
			return fmt.Errorf("%s: bad language %q or text too long", what, lang)
		}
	}
	return nil
}

// Load reads the valid manifests of dir, sorted by id. A missing directory
// has none. Invalid files are reported in skipped and left out, so one broken
// add-on does not hide the others.
func Load(dir string) (manifests []Manifest, skipped []error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, []error{err}
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		m, err := readManifest(filepath.Join(dir, name))
		if err == nil && m.ID+".json" != name {
			err = fmt.Errorf("id %q in file %s", m.ID, name)
		}
		if err == nil && seen[m.ID] {
			err = fmt.Errorf("id %q twice", m.ID)
		}
		if err != nil {
			skipped = append(skipped, fmt.Errorf("%s: %w", name, err))
			continue
		}
		seen[m.ID] = true
		manifests = append(manifests, m)
	}
	sort.Slice(manifests, func(i, j int) bool { return manifests[i].ID < manifests[j].ID })
	return manifests, skipped
}

// readManifest reads a regular file (not a link) of at most 64 KiB.
func readManifest(path string) (Manifest, error) {
	var m Manifest
	if info, err := os.Lstat(path); err != nil {
		return m, err
	} else if !info.Mode().IsRegular() {
		return m, errors.New("not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return m, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, manifestLimit+1))
	if err != nil {
		return m, err
	}
	if len(data) > manifestLimit {
		return m, errors.New("larger than 64 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return m, err
	}
	return m, m.Validate()
}

// Find is the valid manifest of id in dir.
func Find(dir, id string) (Manifest, bool) {
	if !idPattern.MatchString(id) {
		return Manifest{}, false
	}
	m, err := readManifest(filepath.Join(dir, id+".json"))
	if err != nil || m.ID != id {
		return Manifest{}, false
	}
	return m, true
}
