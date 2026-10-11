package router

import (
	"NanoKVM-Server/authn"
	"NanoKVM-Server/common"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/service/failsafe"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestVideoRecoveryRoutes(t *testing.T) {
	// Video disabling is deliberately irreversible inside one server process.
	if os.Getenv("NKOS_TEST_RECOVERY_ROUTES") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestVideoRecoveryRoutes$")
		cmd.Env = append(os.Environ(), "NKOS_TEST_RECOVERY_ROUTES=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("recovery routes: %v\n%s", err, out)
		}
		return
	}
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	t.Setenv("NANOKVM_RECOVERY_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "active"), []byte("1"), 0600); err != nil {
		t.Fatal(err)
	}
	if active, err := failsafe.Init(func() error { return nil }); err != nil || !active {
		t.Fatal(active, err)
	}
	common.DisableVideo()
	store := authn.NewStore(filepath.Join(t.TempDir(), "accounts.json"))
	admin, err := store.SetPassword("admin", "owner-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create("viewer", "valid-password", authn.RoleUser); err != nil {
		t.Fatal(err)
	}
	viewer, err := store.Get("viewer")
	if err != nil {
		t.Fatal(err)
	}
	authn.DefaultStore = store
	at, err := middleware.GenerateJWT(admin.Username, admin.TokenVersion)
	if err != nil {
		t.Fatal(err)
	}
	vt, err := middleware.GenerateJWT(viewer.Username, viewer.TokenVersion)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	vmRouter(r)
	streamRouter(r)
	request := func(method, path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	for _, path := range []string{"/api/stream/mjpeg", "/api/stream/video", "/api/stream/video/direct", "/api/stream/h264", "/api/stream/h264/direct"} {
		if w := request("GET", path, vt); w.Code != 503 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
		if w := request("GET", path, ""); w.Code != 401 {
			t.Fatalf("unauthenticated %s: %d", path, w.Code)
		}
	}
	w := request("GET", "/api/vm/live-status", vt)
	var status struct {
		Code int
		Data struct{ Recovery failsafe.State }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil || status.Code != 0 || !status.Data.Recovery.Active {
		t.Fatalf("management unavailable: %s / %v", w.Body, err)
	}
	for _, tc := range []struct {
		token  string
		status int
	}{{"", 401}, {vt, 403}, {at, 200}} {
		w := request(http.MethodPost, "/api/vm/video/retry", tc.token)
		if w.Code != tc.status {
			t.Fatalf("retry status %d want %d", w.Code, tc.status)
		}
		if tc.status == 200 {
			var rsp struct{ Code int }
			if json.Unmarshal(w.Body.Bytes(), &rsp) != nil || rsp.Code != 0 {
				t.Fatalf("admin retry failed: %s", w.Body)
			}
		}
	}
}
