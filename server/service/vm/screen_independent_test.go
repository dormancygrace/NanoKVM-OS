package vm

import (
	"NanoKVM-Server/common"
	"bytes"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestStreamLimitDoesNotRequireMonitorHardware(t *testing.T) {
	previous := common.GetScreen().Height
	old := screenFileMap["resolution"]
	screenFileMap["resolution"] = filepath.Join(t.TempDir(), "res")
	defer func() { screenFileMap["resolution"] = old; common.SetScreen("resolution", int(previous)) }()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/", bytes.NewBufferString(`{"type":"resolution","value":720}`))
	c.Request.Header.Set("Content-Type", "application/json")
	(&Service{}).SetScreen(c)
	var result struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || result.Code != 0 {
		t.Fatalf("response %s, error %v", recorder.Body.String(), err)
	}
	value, err := os.ReadFile(screenFileMap["resolution"])
	if err != nil || string(value) != "720" {
		t.Fatalf("saved %q, %v", value, err)
	}
	if common.GetScreen().Height != 720 {
		t.Fatal("stream ceiling not published")
	}
}
func TestRejectsInvalidVideoPreferences(t *testing.T) {
	for _, body := range []string{`{"type":"monitor","value":720}`, `{"type":"resolution","value":65536}`, `{"type":"resolution","value":-1}`, `{"type":"fps","value":61}`} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest("POST", "/", bytes.NewBufferString(body))
		c.Request.Header.Set("Content-Type", "application/json")
		(&Service{}).SetScreen(c)
		var result struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || result.Code == 0 {
			t.Fatalf("accepted %s: %s", body, recorder.Body.String())
		}
	}
}
