package router

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/middleware"
	"github.com/gin-gonic/gin"
)

func TestClockRequiresAdministrator(t *testing.T) {
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

	for _, tc := range []struct {
		token string
		want  int
	}{{"", 401}, {viewerToken, 403}, {adminToken, 200}} {
		// Invalid input never sets the host clock, even for the administrator.
		req := httptest.NewRequest(http.MethodPost, "/api/vm/date-time/clock", strings.NewReader(`{"now":0}`))
		if tc.token != "" {
			req.Header.Set("Authorization", "Bearer "+tc.token)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Fatalf("got %d want %d", w.Code, tc.want)
		}
	}
}
