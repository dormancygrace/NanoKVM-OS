package picoclaw

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

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

// liveGateway starts a process that runtimeGatewayPID recognizes as the
// gateway (argv "picoclaw gateway") and records it in the PID file.
func liveGateway(t *testing.T, home string) *exec.Cmd {
	return liveGatewayScript(t, home, "while :; do sleep 0.05; done\n")
}

func liveGatewayScript(t *testing.T, home, script string) *exec.Cmd {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gateway"), []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := &exec.Cmd{Path: "/bin/sh", Args: []string{"picoclaw", "gateway"}, Dir: dir}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
	if err := os.WriteFile(filepath.Join(home, picoclawPIDFileName), []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Start returns after exec, before the child is necessarily observable
	// through procfs. Wait for the fixture before exercising shutdown.
	deadline := time.Now().Add(2 * time.Second)
	for {
		running, err := isRuntimeRunning()
		if err == nil && running {
			break
		}
		select {
		case <-done:
			t.Fatalf("fake gateway exited before becoming ready: %v", cmd.ProcessState)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("fake gateway not recognized: %v %v", running, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cmd
}

func hostToolsRequest(service *Service, enabled bool) string {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/picoclaw/host-tools", strings.NewReader(fmt.Sprintf(`{"enabled":%v}`, enabled)))
	c.Request.Header.Set("Content-Type", "application/json")
	service.UpdateHostTools(c)
	return recorder.Body.String()
}

// A gateway that runs but is not ready keeps the tools it started with
// (hot_reload is off): turning them off must stop it, or fail.
func TestTurningHostToolsOffStopsALiveUnreadyGateway(t *testing.T) {
	gin.SetMode(gin.TestMode)
	home := t.TempDir()
	t.Setenv("PICOCLAW_HOME", home)
	config := filepath.Join(home, "config.json")
	if err := os.WriteFile(config, []byte(`{"agents":{"defaults":{}},"model_list":[],"channel_list":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewRuntimeIntentStore(filepath.Join(t.TempDir(), "picoclaw-runtime.json"))
	previous, previousMCP, previousStop := picoclawHostToolsEnabled, defaultPicoclawMCPServer, stopPicoclawGateway
	picoclawHostToolsEnabled = func() bool {
		intent, err := store.Load()
		return err == nil && intent.HostTools
	}
	defaultPicoclawMCPServer = func() (map[string]any, error) { return map[string]any{"enabled": true}, nil }
	t.Cleanup(func() {
		picoclawHostToolsEnabled, defaultPicoclawMCPServer, stopPicoclawGateway = previous, previousMCP, previousStop
	})
	service := &Service{
		runtime:       &RuntimeStore{status: RuntimeStatus{Status: "unavailable"}},
		runtimeIntent: store,
	}

	// The stop works: success, and no gateway is left.
	gateway := liveGateway(t, home)
	stops := 0
	stopPicoclawGateway = func(*Service) *PicoclawError {
		stops++
		_ = gateway.Process.Kill()
		for i := 0; i < 100; i++ {
			if running, _ := isRuntimeRunning(); !running {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		return nil
	}
	if body := hostToolsRequest(service, false); !strings.Contains(body, `"code":0`) || stops != 1 {
		t.Fatalf("stops %d: %s", stops, body)
	}
	if running, _ := isRuntimeRunning(); running {
		t.Fatal("the old gateway still runs after the tools were turned off")
	}

	// A gateway that takes a moment to exit after TERM is waited for.
	gateway = liveGatewayScript(t, home, "trap 'sleep 0.3; exit 0' TERM\nwhile :; do sleep 0.05; done\n")
	stopPicoclawGateway = func(*Service) *PicoclawError {
		_ = gateway.Process.Signal(syscall.SIGTERM)
		return nil
	}
	if body := hostToolsRequest(service, false); !strings.Contains(body, `"code":0`) {
		t.Fatalf("a slow exit was reported as a failure: %s", body)
	}

	// The stop leaves the process running: an error, not success.
	liveGateway(t, home)
	stopPicoclawGateway = func(*Service) *PicoclawError { return nil }
	if body := hostToolsRequest(service, false); strings.Contains(body, `"code":0`) || !strings.Contains(body, "still running") {
		t.Fatalf("reported success with the gateway alive: %s", body)
	}

	// The config cannot be written: the gateway is stopped anyway, and the
	// request fails.
	gateway = liveGateway(t, home)
	stops = 0
	stopPicoclawGateway = func(*Service) *PicoclawError {
		stops++
		_ = gateway.Process.Kill()
		for i := 0; i < 100; i++ {
			if running, _ := isRuntimeRunning(); !running {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		return nil
	}
	if err := os.WriteFile(config, []byte(`{not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if body := hostToolsRequest(service, false); strings.Contains(body, `"code":0`) || stops != 1 {
		t.Fatalf("stops %d: %s", stops, body)
	}

	// Turning them on does not stop an unready gateway.
	liveGateway(t, home)
	if err := os.WriteFile(config, []byte(`{"agents":{"defaults":{}},"model_list":[],"channel_list":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stops = 0
	if body := hostToolsRequest(service, true); !strings.Contains(body, `"code":0`) || stops != 0 {
		t.Fatalf("stops %d: %s", stops, body)
	}
}
