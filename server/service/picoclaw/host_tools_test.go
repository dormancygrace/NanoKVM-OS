package picoclaw

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func toolSetting(raw map[string]any, path ...string) any {
	var current any = raw
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[key]
	}
	return current
}

// The agent's tools on the NanoKVM itself are off unless the administrator
// turns them on: the shell, file writes and scheduled commands, and the
// agent stays in its workspace with the shell's deny patterns on.
func TestHostToolsAreOffByDefault(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		editor := &picoclawConfigEditor{raw: map[string]any{
			// An earlier NanoKVM wrote the permissive values.
			"tools":  map[string]any{"exec": map[string]any{"enable_deny_patterns": false}},
			"agents": map[string]any{"defaults": map[string]any{"restrict_to_workspace": false}},
		}}
		for _, entry := range picoclawHostToolDefaults(enabled) {
			editor.setValue(entry.value, entry.path...)
		}
		for _, path := range [][]string{
			{"tools", "exec", "enabled"}, {"tools", "exec", "allow_remote"},
			{"tools", "write_file", "enabled"}, {"tools", "edit_file", "enabled"},
			{"tools", "append_file", "enabled"}, {"tools", "cron", "allow_command"},
			{"agents", "defaults", "allow_read_outside_workspace"},
		} {
			if got := toolSetting(editor.raw, path...); got != enabled {
				t.Errorf("host tools %v: %v = %v", enabled, path, got)
			}
		}
		for _, path := range [][]string{{"tools", "exec", "enable_deny_patterns"}, {"agents", "defaults", "restrict_to_workspace"}} {
			if got := toolSetting(editor.raw, path...); got != !enabled {
				t.Errorf("host tools %v: %v = %v", enabled, path, got)
			}
		}
	}
}

func TestHostToolsSettingIsStored(t *testing.T) {
	store := NewRuntimeIntentStore(filepath.Join(t.TempDir(), "picoclaw-runtime.json"))
	if intent, err := store.Load(); err != nil || intent.HostTools {
		t.Fatalf("new intent: %+v %v", intent, err)
	}
	if err := store.SetDesiredRunning(true, "test"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetHostTools(true, "test"); err != nil {
		t.Fatal(err)
	}
	intent, err := store.Load()
	if err != nil || !intent.HostTools || !intent.DesiredRunning {
		t.Fatalf("stored intent: %+v %v", intent, err)
	}
}

// The switch has its own endpoint: it stores the choice and rewrites the
// tool settings without the model's API key.
func TestUpdateHostToolsRewritesTheConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	home := t.TempDir()
	t.Setenv("PICOCLAW_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(`{"agents":{"defaults":{}},"model_list":[],"channel_list":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewRuntimeIntentStore(filepath.Join(t.TempDir(), "picoclaw-runtime.json"))
	previous := picoclawHostToolsEnabled
	picoclawHostToolsEnabled = func() bool {
		intent, err := store.Load()
		return err == nil && intent.HostTools
	}
	previousMCP := defaultPicoclawMCPServer
	defaultPicoclawMCPServer = func() (map[string]any, error) { return map[string]any{"enabled": true}, nil }
	t.Cleanup(func() { picoclawHostToolsEnabled, defaultPicoclawMCPServer = previous, previousMCP })
	service := &Service{
		runtime:       &RuntimeStore{status: RuntimeStatus{Status: "stopped"}},
		runtimeIntent: store,
	}

	for _, enabled := range []bool{true, false} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		body := fmt.Sprintf(`{"enabled":%v}`, enabled)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/picoclaw/host-tools", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		service.UpdateHostTools(c)
		if !strings.Contains(recorder.Body.String(), `"code":0`) {
			t.Fatalf("enabled %v: %s", enabled, recorder.Body.String())
		}
		if intent, err := store.Load(); err != nil || intent.HostTools != enabled {
			t.Fatalf("enabled %v: stored %+v %v", enabled, intent, err)
		}
		doc, err := loadPicoclawConfigDocument()
		if err != nil {
			t.Fatal(err)
		}
		if got := toolSetting(doc.raw, "tools", "exec", "enabled"); got != enabled {
			t.Fatalf("enabled %v: exec %v", enabled, got)
		}
	}

	// Before PicoClaw has a config the choice is stored for its first start.
	if err := os.Remove(filepath.Join(home, "config.json")); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/picoclaw/host-tools", strings.NewReader(`{"enabled":true}`))
	c.Request.Header.Set("Content-Type", "application/json")
	service.UpdateHostTools(c)
	if intent, err := store.Load(); !strings.Contains(recorder.Body.String(), `"code":0`) || err != nil || !intent.HostTools {
		t.Fatalf("without a config: %s %+v %v", recorder.Body.String(), intent, err)
	}

	recorder = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/picoclaw/host-tools", strings.NewReader(`{}`))
	c.Request.Header.Set("Content-Type", "application/json")
	service.UpdateHostTools(c)
	if strings.Contains(recorder.Body.String(), `"code":0`) {
		t.Fatalf("accepted a request without enabled: %s", recorder.Body.String())
	}
}
