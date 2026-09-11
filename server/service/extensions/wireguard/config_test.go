package wireguard

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

const testKey = "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="
const testConfig = "[Interface]\nPrivateKey = " + testKey + "\nAddress = 10.20.0.2/32, fd00::2/128\nDNS = 1.1.1.1\n[Peer]\nPublicKey = " + testKey + "\nEndpoint = vpn.example.com:51820\nAllowedIPs = 0.0.0.0/0, ::/0\nPersistentKeepalive = 25\n"

func TestValidate(t *testing.T) {
	if _, err := Validate(testConfig); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		strings.Replace(testConfig, "DNS = 1.1.1.1", "PostUp = touch /tmp/executed", 1),
		strings.Replace(testConfig, "DNS = 1.1.1.1", "SaveConfig = true", 1),
		strings.Replace(testConfig, "DNS = 1.1.1.1", "DNS = $(id)", 1),
		strings.Replace(testConfig, "vpn.example.com:51820", "$(id):51820", 1),
		strings.Replace(testConfig, "10.20.0.2/32", "10.20.0.2/99", 1),
		strings.Replace(testConfig, "PersistentKeepalive = 25", "PersistentKeepalive = -1", 1),
		testConfig + "[Peer]\nPublicKey = " + testKey + "\n",
		testConfig + "[Interface]\nPrivateKey = " + testKey + "\n",
		strings.Repeat("x", 65537), testConfig + "\x00",
	} {
		if _, e := Validate(bad); e == nil {
			t.Error("accepted invalid configuration")
		} else if strings.Contains(e.Error(), testKey) {
			t.Error("key leaked in error")
		}
	}
}

func TestImportTransactionAndPrivateStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Service{dir: filepath.Join(t.TempDir(), "profiles"), failures: map[string]string{}}
	importFiles := func(configs ...string) string {
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		for _, cfg := range configs {
			f, _ := w.CreateFormFile("files", "home.conf")
			_, _ = f.Write([]byte(cfg))
		}
		_ = w.Close()
		r := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(r)
		c.Request = httptest.NewRequest("POST", "/", &body)
		c.Request.Header.Set("Content-Type", w.FormDataContentType())
		s.Import(c)
		return r.Body.String()
	}
	importFiles(testConfig, strings.Replace(testConfig, "DNS = 1.1.1.1", "PostUp = false", 1))
	p, e := s.profiles()
	if e != nil || len(p) != 0 {
		t.Fatal("invalid batch partially committed")
	}
	body := importFiles(testConfig, testConfig)
	var response struct {
		Code int `json:"code"`
	}
	if json.Unmarshal([]byte(body), &response) != nil || response.Code != 0 {
		t.Fatal(body)
	}
	p, e = s.profiles()
	if e != nil || len(p) != 2 {
		t.Fatal("batch not committed")
	}
	if s.desired() != "" {
		t.Fatal("import activated a tunnel")
	}
	for _, profile := range p {
		st, err := os.Stat(s.config(profile.ID))
		if err != nil || st.Mode().Perm() != 0600 {
			t.Fatal("config permissions")
		}
	}
	st, _ := os.Stat(s.dir)
	if st.Mode().Perm() != 0700 {
		t.Fatal("directory permissions")
	}
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	c.Request = httptest.NewRequest("GET", "/", nil)
	s.GetStatus(c)
	if strings.Contains(r.Body.String(), testKey) || strings.Contains(r.Body.String(), "PrivateKey") {
		t.Fatal("status leaked credentials")
	}
	// Delete an inactive profile without calling any networking commands.
	r = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(r)
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(`{"id":"`+p[0].ID+`","action":"delete"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	s.Change(c)
	left, _ := s.profiles()
	if len(left) != 1 {
		t.Fatal("delete failed", r.Body.String())
	}
	if _, e = os.Stat(s.config(p[0].ID)); !os.IsNotExist(e) {
		t.Fatal("deleted key remains")
	}
}

func TestAllowedIPsDoNotCreateRoutes(t *testing.T) {
	for _, table := range []string{"", "Table = auto\n", "Table = off\n", "Table = 1234\n"} {
		t.Run(strings.TrimSpace(table), func(t *testing.T) {
			input := strings.Replace(testConfig, "[Interface]\n", "[Interface]\n"+table, 1)
			cfg, err := Validate(input)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(cfg, "Table = ") != 1 || !strings.Contains(cfg, "[Interface]\nTable = off\n") {
				t.Fatal("profile permits routes from AllowedIPs")
			}
			for _, line := range []string{"Address = 10.20.0.2/32, fd00::2/128\n", "AllowedIPs = 0.0.0.0/0, ::/0\n"} {
				if !strings.Contains(cfg, line) {
					t.Fatal("interface or peer prefixes changed")
				}
			}
			again, err := Validate(cfg)
			if err != nil || again != cfg {
				t.Fatal("normalization is not stable")
			}
		})
	}
}

func TestRoutingChoicePersistsAndRequiresInactiveProfile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	s := &Service{dir: dir, failures: map[string]string{}}
	id := "nk0000000000"
	if err := s.save([]Profile{{ID: id, Name: "lab"}}); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{true, false} {
		r := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(r)
		data, _ := json.Marshal(map[string]any{"id": id, "action": "routing", "routeAllowedIPs": enabled})
		c.Request = httptest.NewRequest("POST", "/", bytes.NewReader(data))
		c.Request.Header.Set("Content-Type", "application/json")
		s.Change(c)
		var rsp struct {
			Code int `json:"code"`
		}
		if json.Unmarshal(r.Body.Bytes(), &rsp) != nil || rsp.Code != 0 {
			t.Fatal(r.Body.String())
		}
		restarted := &Service{dir: dir}
		profiles, err := restarted.profiles()
		if err != nil || profiles[0].RouteAllowedIPs != enabled {
			t.Fatal("routing choice not persisted")
		}
		cfg, err := profileConfig(testConfig, profiles[0].RouteAllowedIPs)
		if err != nil {
			t.Fatal(err)
		}
		table := "off"
		if enabled {
			table = "auto"
		}
		if !strings.Contains(cfg, "Table = "+table+"\n") || !strings.Contains(cfg, "AllowedIPs = 0.0.0.0/0, ::/0\n") {
			t.Fatal("routing choice not applied or AllowedIPs changed")
		}
	}
	// Also protect a profile scheduled for restore even before its link exists.
	if err := atomicWrite(filepath.Join(dir, "active"), []byte(id)); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(`{"id":"nk0000000000","action":"routing","routeAllowedIPs":true}`))
	c.Request.Header.Set("Content-Type", "application/json")
	s.Change(c)
	var rsp struct {
		Code int `json:"code"`
	}
	if json.Unmarshal(r.Body.Bytes(), &rsp) != nil || rsp.Code == 0 {
		t.Fatal("active profile routing changed")
	}
	profiles, _ := s.profiles()
	if profiles[0].RouteAllowedIPs {
		t.Fatal("rejected change was saved")
	}
}
