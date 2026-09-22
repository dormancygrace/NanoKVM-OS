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
	admin, ok, err := store.Authenticate("admin", "admin")
	if err != nil || !ok {
		t.Fatalf("admin account: ok=%v err=%v", ok, err)
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
	for _, path := range []string{"/api/vm/diagnostics", "/api/vm/diagnostics/report"} {
		if status := diagnosticsRequest(r, path, viewerToken); status != http.StatusForbidden {
			t.Fatalf("viewer %s = %d, want 403", path, status)
		}
		if status := diagnosticsRequest(r, path, adminToken); status != http.StatusOK {
			t.Fatalf("admin %s = %d, want 200", path, status)
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
