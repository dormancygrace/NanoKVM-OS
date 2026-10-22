package vm

import (
	"NanoKVM-Server/authn"
	"NanoKVM-Server/middleware"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportSSHKeysHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	home := t.TempDir()
	store := authn.NewStore(filepath.Join(t.TempDir(), "accounts.json"))
	admin, err := store.SetPassword("admin", "owner-password")
	if err != nil {
		t.Fatal(err)
	}
	oldStore := authn.DefaultStore
	authn.DefaultStore = store
	t.Cleanup(func() { authn.DefaultStore = oldStore })
	token, err := middleware.GenerateJWT(admin.Username, admin.TokenVersion)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	oldOutput := log.StandardLogger().Out
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(oldOutput) })
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Request.Header.Set("Authorization", "Bearer "+token) })
	r.Use(middleware.CheckToken(), middleware.RequireRole(authn.RoleAdmin))
	r.POST("/import", func(c *gin.Context) { importSSHKeys(c, home) })
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	key, _ := ssh.NewPublicKey(pub)
	text := string(ssh.MarshalAuthorizedKey(key))
	for _, body := range []string{`{`, `{"keys":"invalid"}`, `{"keys":42}`} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", "/import", strings.NewReader(body)))
		var response struct{ Code int }
		json.Unmarshal(w.Body.Bytes(), &response)
		if response.Code == 0 {
			t.Fatal("accepted invalid body")
		}
	}
	for _, password := range []string{"", "wrong-password"} {
		body, _ := json.Marshal(map[string]string{"keys": text, "password": password})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", "/import", strings.NewReader(string(body))))
		var response struct{ Code int }
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Code != -3 {
			t.Fatal(w.Body.String())
		}
		if _, err := os.Stat(filepath.Join(home, ".ssh")); !os.IsNotExist(err) {
			t.Fatal("failed reauthentication touched SSH files")
		}
	}
	for _, want := range []int{1, 0} {
		body, _ := json.Marshal(map[string]string{"keys": text, "password": "owner-password"})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", "/import", strings.NewReader(string(body))))
		var response struct {
			Code int
			Data struct {
				Added   int
				Skipped int
			}
		}
		if e := json.Unmarshal(w.Body.Bytes(), &response); e != nil {
			t.Fatal(e)
		}
		if response.Code != 0 || response.Data.Added != want || response.Data.Skipped != 1-want {
			t.Fatal(w.Body.String())
		}
	}
	if !strings.Contains(logs.String(), ssh.FingerprintSHA256(key)) || !strings.Contains(logs.String(), "admin") || strings.Contains(logs.String(), "owner-password") || strings.Contains(logs.String(), strings.TrimSpace(text)) {
		t.Fatal("audit log lacks attribution or contains credentials")
	}
	content, _ := os.ReadFile(filepath.Join(home, ".ssh/authorized_keys"))
	if string(content) != text {
		t.Fatal("incorrect authorized_keys")
	}
}
