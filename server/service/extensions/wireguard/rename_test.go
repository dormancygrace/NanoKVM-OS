package wireguard

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRenamePreservesActiveTunnel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	id := "nk0000000000"
	s := &Service{dir: dir, failures: map[string]string{id: "prior connection error"}}
	if err := s.save([]Profile{{ID: id, Name: "original", RouteAllowedIPs: true}}); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(s.config(id), []byte(testConfig)); err != nil {
		t.Fatal(err)
	}
	active := []byte(id + "\n")
	if err := atomicWrite(filepath.Join(dir, "active"), active); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir()) // Renaming must require no networking commands.
	call := func(name *string) int {
		data, _ := json.Marshal(map[string]any{"id": id, "action": "rename", "name": name})
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/", bytes.NewReader(data))
		c.Request.Header.Set("Content-Type", "application/json")
		s.Change(c)
		var result struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.Code
	}
	name := "  Домашний VPN 🌍  "
	if call(&name) != 0 {
		t.Fatal("rename failed")
	}
	reopened := &Service{dir: dir}
	profiles, err := reopened.profiles()
	if err != nil || len(profiles) != 1 || profiles[0].Name != "Домашний VPN 🌍" || profiles[0].ID != id || !profiles[0].RouteAllowedIPs {
		t.Fatal(profiles, err)
	}
	cfg, _ := os.ReadFile(s.config(id))
	if string(cfg) != testConfig {
		t.Fatal("tunnel config changed")
	}
	actual, _ := os.ReadFile(filepath.Join(dir, "active"))
	if !bytes.Equal(active, actual) {
		t.Fatal("boot restore marker changed")
	}
	if s.failures[id] != "prior connection error" {
		t.Fatal("connection state cleared")
	}
	for _, bad := range []string{"", "   ", "bad\nname", "bad\x00name", strings.Repeat("я", 129)} {
		if call(&bad) == 0 {
			t.Fatalf("accepted %q", bad)
		}
	}
	if call(nil) == 0 {
		t.Fatal("missing name accepted")
	}
	profiles, _ = reopened.profiles()
	if profiles[0].Name != "Домашний VPN 🌍" {
		t.Fatal("invalid rename modified the name")
	}
}
