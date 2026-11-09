package mcpservice

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"testing"

	"NanoKVM-Server/service/controlmode"

	"github.com/gin-gonic/gin"
)

func useTestConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mcp.json")
	oldPath := configFilePath
	configFilePath = path
	t.Cleanup(func() { configFilePath = oldPath })
	return path
}

func TestLoadConfigRejectsCorruptJSON(t *testing.T) {
	path := useTestConfig(t)
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(); err == nil {
		t.Fatal("expected corrupt config error")
	}
}

func TestAPIKeyMiddleware(t *testing.T) {
	useTestConfig(t)
	control := controlmode.NewManager(filepath.Join(t.TempDir(), "mode"), controlmode.ModeOff)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	created := 0
	handler := lazyMCPHandler(func() http.Handler {
		created++
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	})
	router.Any("/api/mcp", APIKeyMiddleware(control), gin.WrapH(http.NewCrossOriginProtection().Handler(handler)))

	writeConfig := func(cfg Config) {
		if _, err := updateConfig(func(Config) (Config, error) { return cfg, nil }); err != nil {
			t.Fatal(err)
		}
	}
	request := func(header string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/mcp", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		router.ServeHTTP(recorder, req)
		return recorder
	}

	writeConfig(Config{APIKey: "secret"})
	if got := request("Bearer secret").Code; got != http.StatusForbidden {
		t.Fatalf("disabled status = %d", got)
	}
	if err := control.SwitchWithCleanup(controlmode.ModeMCP, nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, header := range []string{"", "secret", "Basic secret", "Bearer wrong"} {
		if got := request(header).Code; got != http.StatusUnauthorized {
			t.Fatalf("header %q status = %d", header, got)
		}
	}
	blocked := httptest.NewRecorder()
	crossOrigin := httptest.NewRequest(http.MethodPost, "http://nanokvm.local/api/mcp", nil)
	crossOrigin.Header.Set("Authorization", "Bearer secret")
	crossOrigin.Header.Set("Origin", "https://evil.example")
	router.ServeHTTP(blocked, crossOrigin)
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status=%d", blocked.Code)
	}
	if created != 0 {
		t.Fatal("disabled/unauthenticated/cross-origin MCP initialized handler")
	}
	if got := request("Bearer secret").Code; got != http.StatusNoContent {
		t.Fatalf("valid key status = %d", got)
	}
	if created != 1 {
		t.Fatalf("initialized %d times", created)
	}
	writeConfig(Config{APIKey: "rotated"})
	if got := request("Bearer secret").Code; got != http.StatusUnauthorized {
		t.Fatalf("old key after rotation status = %d", got)
	}
	if got := request("Bearer rotated").Code; got != http.StatusNoContent {
		t.Fatalf("rotated key status = %d", got)
	}
}
