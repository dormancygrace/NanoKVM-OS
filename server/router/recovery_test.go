package router

import (
	"NanoKVM-Server/authn"
	"NanoKVM-Server/middleware"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecoveryAuthorization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := authn.NewStore(filepath.Join(t.TempDir(), "accounts.json"))
	admin, err := store.SetPassword("admin", "owner-password")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Create("viewer", "viewer-password", authn.RoleUser); err != nil {
		t.Fatal(err)
	}
	viewer, err := store.Get("viewer")
	if err != nil {
		t.Fatal(err)
	}
	old := authn.DefaultStore
	authn.DefaultStore = store
	t.Cleanup(func() { authn.DefaultStore = old })
	adminToken, err := middleware.GenerateJWT(admin.Username, admin.TokenVersion)
	if err != nil {
		t.Fatal(err)
	}
	viewerToken, err := middleware.GenerateJWT(viewer.Username, viewer.TokenVersion)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	recoveryRouter(r)
	for _, action := range []string{"export", "preview", "apply", "reboot"} {
		for _, tc := range []struct {
			token  string
			status int
		}{{"", 401}, {viewerToken, 403}} {
			req := httptest.NewRequest(http.MethodPost, "/api/system/recovery/"+action, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+tc.token)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("%s = %d", action, w.Code)
			}
		}
		// Even a valid admin session cannot export secrets or queue destruction
		// without reauthentication; this exits before touching the real filesystem.
		req := httptest.NewRequest(http.MethodPost, "/api/system/recovery/"+action, strings.NewReader(`{"password":"wrong-password"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+adminToken)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if !strings.Contains(w.Body.String(), "current administrator password is required") {
			t.Fatal(w.Body.String())
		}
	}
}
