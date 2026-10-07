package router

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/common"
	"NanoKVM-Server/middleware"
	"github.com/gin-gonic/gin"
)

func TestFrameDetectIsAdminOnlyAndPersisted(t *testing.T) {
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
	oldFile := common.FrameDetectFile
	common.FrameDetectFile = filepath.Join(t.TempDir(), "frame_detect")
	t.Cleanup(func() { common.FrameDetectFile = oldFile })
	adminToken, err := middleware.GenerateJWT(admin.Username, admin.TokenVersion)
	if err != nil {
		t.Fatal(err)
	}
	viewerToken, err := middleware.GenerateJWT(viewer.Username, viewer.TokenVersion)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	streamRouter(r)

	post := func(token, body string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/stream/mjpeg/detect", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		writer := httptest.NewRecorder()
		r.ServeHTTP(writer, req)
		return writer.Code
	}

	if common.FrameDetectEnabled() {
		t.Fatal("frame detect must default to off when nothing is saved")
	}
	if status := post(viewerToken, `{"enabled":true}`); status != http.StatusForbidden {
		t.Fatalf("viewer update = %d, want 403", status)
	}
	if common.FrameDetectEnabled() {
		t.Fatal("a rejected viewer request changed the saved choice")
	}
	if status := post(adminToken, `{"enabled":true}`); status != http.StatusOK {
		t.Fatalf("admin enable = %d, want 200", status)
	}
	if !common.FrameDetectEnabled() {
		t.Fatal("enabling frame detect was not persisted")
	}
	if status := post(adminToken, `{"enabled":false}`); status != http.StatusOK {
		t.Fatalf("admin disable = %d, want 200", status)
	}
	if common.FrameDetectEnabled() {
		t.Fatal("disabling frame detect was not persisted")
	}
}
