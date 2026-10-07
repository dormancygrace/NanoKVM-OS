package vm

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestLiveStatusShowsUSBOnlyToAdministrators(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, admin := range []bool{false, true} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/vm/live-status", nil)
		(&Service{}).GetLiveStatus(c, admin)

		var body struct {
			Code int                        `json:"code"`
			Data map[string]json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Code != 0 {
			t.Fatalf("admin=%v: %v %s", admin, err, w.Body.String())
		}
		for _, part := range []string{"hdmi", "input", "audio"} {
			if string(body.Data[part]) == "null" || body.Data[part] == nil {
				t.Errorf("admin=%v: %s missing", admin, part)
			}
		}
		if hasUSB := string(body.Data["usb"]) != "null"; hasUSB != admin {
			t.Errorf("admin=%v: usb=%s", admin, body.Data["usb"])
		}
	}
}
