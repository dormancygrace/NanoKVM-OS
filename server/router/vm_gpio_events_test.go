package router

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/config"
	"NanoKVM-Server/middleware"
	"github.com/gin-gonic/gin"
)

func TestGPIOEventsRequireSessionButNotInputOwnership(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := authn.NewStore(filepath.Join(t.TempDir(), "accounts.json"))
	if _, err := store.SetPassword("admin", "owner-password"); err != nil {
		t.Fatal(err)
	}
	if err := store.Create("viewer", "valid-password", authn.RoleUser); err != nil {
		t.Fatal(err)
	}
	viewer, err := store.Get("viewer")
	if err != nil {
		t.Fatal(err)
	}
	oldStore, hardware := authn.DefaultStore, config.GetInstance().Hardware
	authn.DefaultStore = store
	// No hardware: an authorized request stops at HTTP 204, without GPIO I/O.
	config.GetInstance().Hardware.GPIOPower = ""
	t.Cleanup(func() { authn.DefaultStore = oldStore; config.GetInstance().Hardware = hardware })
	token, err := middleware.GenerateJWT(viewer.Username, viewer.TokenVersion)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	vmRouter(r)
	for _, test := range []struct {
		token string
		want  int
	}{{"", http.StatusUnauthorized}, {"invalid", http.StatusUnauthorized}, {token, http.StatusNoContent}} {
		req := httptest.NewRequest("GET", "/api/vm/gpio/events", nil)
		if test.token != "" {
			req.Header.Set("Authorization", "Bearer "+test.token)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != test.want {
			t.Fatalf("LED stream status = %d, want %d", w.Code, test.want)
		}
	}
}
