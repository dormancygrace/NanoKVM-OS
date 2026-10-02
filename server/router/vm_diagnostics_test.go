package router

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/middleware"
	"github.com/gin-gonic/gin"
)

func TestDiagnosticsEndpointsRequireAdministrator(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := authn.NewStore(filepath.Join(t.TempDir(), "accounts.json"))
	admin, err := store.SetPassword("admin", "owner-password")
	if err != nil {
		t.Fatalf("admin account: %v", err)
	}
	if err := store.Create("viewer", "valid-password", authn.RoleUser); err != nil {
		t.Fatal(err)
	}
	viewer, err := store.Get("viewer")
	if err != nil {
		t.Fatal(err)
	}
	oldStore := authn.DefaultStore
	authn.DefaultStore = store
	t.Cleanup(func() { authn.DefaultStore = oldStore })
	adminToken, err := middleware.GenerateJWT(admin.Username, admin.TokenVersion)
	if err != nil {
		t.Fatal(err)
	}
	viewerToken, err := middleware.GenerateJWT(viewer.Username, viewer.TokenVersion)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	vmRouter(r)
	for _, path := range []string{"/api/vm/diagnostics", "/api/vm/diagnostics/report", "/api/vm/logs", "/api/vm/logs/boots"} {
		if status := diagnosticsRequest(r, path, ""); status != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s = %d, want 401", path, status)
		}
		if status := diagnosticsRequest(r, path, viewerToken); status != http.StatusForbidden {
			t.Fatalf("viewer %s = %d, want 403", path, status)
		}
		if status := diagnosticsRequest(r, path, adminToken); status != http.StatusOK {
			t.Fatalf("admin %s = %d, want 200", path, status)
		}
	}
	for _, path := range []string{"/api/vm/logs?source=../../etc/shadow", "/api/vm/logs?boot=current/../previous", "/api/vm/logs?source=kernel;id"} {
		if status := diagnosticsRequest(r, path, adminToken); status != http.StatusBadRequest {
			t.Fatalf("invalid log selector %s returned %d", path, status)
		}
	}
}

func diagnosticsRequest(r http.Handler, path, token string) int {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	writer := httptest.NewRecorder()
	r.ServeHTTP(writer, req)
	return writer.Code
}
