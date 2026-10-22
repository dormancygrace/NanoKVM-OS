package vm

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"github.com/gin-gonic/gin"
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
	r := gin.New()
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
	for _, want := range []int{1, 0} {
		body, _ := json.Marshal(map[string]string{"keys": text})
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
	content, _ := os.ReadFile(filepath.Join(home, ".ssh/authorized_keys"))
	if string(content) != text {
		t.Fatal("incorrect authorized_keys")
	}
}
