package addons

import (
	"encoding/json"
	"os"
	"path/filepath"

	"testing"
)

func validManifest() Manifest {
	return Manifest{
		ID: "demo", API: APIVersion,
		Title:   Text{"en": "Demo", "ru": "Демо"},
		Backend: &Backend{Socket: "demo.sock"},
		Settings: &Settings{Group: "system", Icon: "server", Form: Form{
			Source: "/api/addon/demo/settings", Submit: "/api/addon/demo/settings", Refresh: 5,
			Fields: []Field{
				{Key: "enabled", Type: FieldSwitch, Label: Text{"en": "Enabled"}},
				{Key: "mode", Type: FieldSelect, Label: Text{"en": "Mode"},
					Options: []Option{{Value: "a", Label: Text{"en": "A"}}}},
				{Key: "state", Type: FieldStatus, Label: Text{"en": "State"}},
			},
			Actions: []Action{{ID: "restart", Label: Text{"en": "Restart"}, URL: "/api/addon/demo/restart"}},
		}},
	}
}

func TestManifestValidate(t *testing.T) {
	valid := validManifest()
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid manifest: %v", err)
	}
	statusOnly := validManifest()
	statusOnly.Settings.Form.Submit = ""
	statusOnly.Settings.Form.Fields = []Field{{Key: "state", Type: FieldStatus, Label: Text{"en": "State"}}}
	if err := statusOnly.Validate(); err != nil {
		t.Fatalf("status-only form: %v", err)
	}
	section := validManifest()
	section.Settings.Group, section.Settings.Section, section.Settings.Icon = "", "services-bmc", ""
	if err := section.Validate(); err != nil {
		t.Fatalf("section of a core page: %v", err)
	}
	noPage := Manifest{ID: "x", API: APIVersion, Title: Text{"en": "X"}}
	if err := noPage.Validate(); err != nil {
		t.Fatalf("manifest without page or backend: %v", err)
	}
	for name, change := range map[string]func(*Manifest){
		"bad id":              func(m *Manifest) { m.ID = "../x" },
		"upper-case id":       func(m *Manifest) { m.ID = "Demo" },
		"other api":           func(m *Manifest) { m.API = 2 },
		"no English title":    func(m *Manifest) { m.Title = Text{"ru": "Демо"} },
		"socket with a slash": func(m *Manifest) { m.Backend.Socket = "../app.sock" },
		"socket not .sock":    func(m *Manifest) { m.Backend.Socket = "demo" },
		"unknown group":       func(m *Manifest) { m.Settings.Group = "secret" },
		"no group or section": func(m *Manifest) { m.Settings.Group = "" },
		"group and section":   func(m *Manifest) { m.Settings.Section = "services-bmc" },
		"unknown section": func(m *Manifest) {
			m.Settings.Group, m.Settings.Icon, m.Settings.Section = "", "", "system-users"
		},
		"icon on a section": func(m *Manifest) { m.Settings.Group, m.Settings.Section = "", "services-bmc" },
		"form without backend": func(m *Manifest) {
			m.Backend = nil
		},
		"source of the server": func(m *Manifest) { m.Settings.Form.Source = "/api/auth/password" },
		"source of another add-on": func(m *Manifest) {
			m.Settings.Form.Source = "/api/addon/other/settings"
		},
		"dot-dot in submit": func(m *Manifest) { m.Settings.Form.Submit = "/api/addon/demo/../../auth/password" },
		"query in action": func(m *Manifest) {
			m.Settings.Form.Actions[0].URL = "/api/addon/demo/x?y=1"
		},
		"editable fields without submit": func(m *Manifest) { m.Settings.Form.Submit = "" },
		"unknown field type":             func(m *Manifest) { m.Settings.Form.Fields[0].Type = "html" },
		"repeated key":                   func(m *Manifest) { m.Settings.Form.Fields[1].Key = "enabled" },
		"select without options":         func(m *Manifest) { m.Settings.Form.Fields[1].Options = nil },
		"options on a switch": func(m *Manifest) {
			m.Settings.Form.Fields[0].Options = []Option{{Value: "a", Label: Text{"en": "A"}}}
		},
		"min on a switch": func(m *Manifest) {
			one := 1.0
			m.Settings.Form.Fields[0].Min = &one
		},
		"refresh of 1 s": func(m *Manifest) { m.Settings.Form.Refresh = 1 },
		"bad icon":       func(m *Manifest) { m.Settings.Icon = "<svg>" },
	} {
		m := validManifest()
		change(&m)
		if err := m.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// The add-ons of this repository ship manifests the server accepts.

func writeManifest(t *testing.T, dir, name string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// named-wrong (id demo), bad (unknown field), broken, huge, link.

func TestManifestURLBrowserNormalization(t *testing.T) {
	invalid := []string{
		"/api/addon/demo/%2e%2e/%2e%2e/auth/password",
		"/api/addon/demo/.%2E/.%2e/auth/password",
		"/api/addon/demo/.\t./.\n./auth/password",
		"/api/addon/demo/status%0a",
		"/api/addon/demo/%5cstatus",
		"/api/addon/demo/status?mode=1",
		"/api/addon/demo/status#fragment",
		"/api/addon/demo/%zz",
	}
	for _, target := range []string{"source", "submit", "action"} {
		for _, value := range invalid {
			t.Run(target+"/"+value, func(t *testing.T) {
				m := validManifest()
				switch target {
				case "source":
					m.Settings.Form.Source = value
				case "submit":
					m.Settings.Form.Submit = value
				case "action":
					m.Settings.Form.Actions[0].URL = value
				}
				if err := m.Validate(); err == nil {
					t.Fatalf("accepted unsafe URL %q", value)
				}
			})
		}
	}
	for _, value := range []string{"/api/addon/demo/status", "/api/addon/demo/status%2Ddetail", "/api/addon/demo/%E2%9C%93"} {
		m := validManifest()
		m.Settings.Form.Source = value
		if err := m.Validate(); err != nil {
			t.Errorf("valid URL %q: %v", value, err)
		}
	}
}
