package addons

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
func TestRepositoryManifests(t *testing.T) {
	paths, err := filepath.Glob("../../../addons/*/addon-ui.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no add-on manifests found: %v", err)
	}
	for _, path := range paths {
		dir := t.TempDir()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var m Manifest
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if err := os.WriteFile(filepath.Join(dir, m.ID+".json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
		if manifests, skipped := Load(dir); len(manifests) != 1 || len(skipped) != 0 {
			t.Errorf("%s: %v", path, skipped)
		}
	}
}

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

func TestLoadSkipsBrokenManifests(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "demo.json", validManifest())
	other := validManifest()
	other.ID = "zeta"
	other.Backend.Socket = "zeta.sock"
	other.Settings = nil
	writeManifest(t, dir, "zeta.json", other)
	writeManifest(t, dir, "named-wrong.json", validManifest())
	writeManifest(t, dir, "bad.json", map[string]any{"id": "bad", "api": 1, "title": map[string]string{"en": "Bad"}, "unknown": 1})
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "huge.json"), []byte(strings.Repeat(" ", manifestLimit+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "demo.json"), filepath.Join(dir, "link.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("not a manifest"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifests, skipped := Load(dir)
	if len(manifests) != 2 || manifests[0].ID != "demo" || manifests[1].ID != "zeta" {
		t.Fatalf("manifests = %+v", manifests)
	}
	// named-wrong (id demo), bad (unknown field), broken, huge, link.
	if len(skipped) != 5 {
		t.Fatalf("skipped = %v, want 5", skipped)
	}
	if m, ok := Find(dir, "demo"); !ok || m.Backend.Socket != "demo.sock" {
		t.Fatalf("Find(demo) = %+v, %v", m, ok)
	}
	for _, id := range []string{"link", "bad", "../demo", "missing"} {
		if _, ok := Find(dir, id); ok {
			t.Errorf("Find(%q) succeeded", id)
		}
	}
	if manifests, skipped := Load(filepath.Join(dir, "absent")); manifests != nil || skipped != nil {
		t.Errorf("missing directory: %v, %v", manifests, skipped)
	}
}
