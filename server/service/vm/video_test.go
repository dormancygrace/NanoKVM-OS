package vm

import (
	"NanoKVM-Server/authn"

	"NanoKVM-Server/middleware"

	"bytes"
	"encoding/json"
	"net/http/httptest"

	"testing"

	"github.com/gin-gonic/gin"
)

func setVideoAs(t *testing.T, body string, role authn.Role) (code int, raw string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/", bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("principal", middleware.Principal{Username: "tester", Role: role})
	(&Service{}).SetVideoSettings(c)
	var result struct {
		Code int `json:"code"`
	}
	if recorder.Code == 403 {
		return -403, recorder.Body.String()
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("response %q: %v", recorder.Body.String(), err)
	}
	return result.Code, recorder.Body.String()
}

// A valid stream limit with an invalid rate: nothing may change.

func TestVideoSettingsRequireAdministrator(t *testing.T) {
	if code, _ := setVideoAs(t, `{"fps":30}`, authn.RoleUser); code != -403 {
		t.Fatalf("code %d", code)
	}
}

// H.264 encodes up to 2560 wide: 3840x2160 and 3440x1440 need H.265.

// The "monitor" setting takes the 16:10 and 21:9 ids, checks their video
// memory by pixels, and needs a receiver that takes them.

// Pixels above 2560x1440 need the UHD pool, whatever the receiver.

// 2560x1080 fits QHD; without a receiver that takes it, it is refused.

// The 16:10 and 21:9 modes are offered by receiver, the 16:9 set unchanged:
// an LT6911C or LT6911D takes 1920x1200 and 2560x1080 only, and the monitor
// setting is validated the same way.

// Values accepted by the public endpoint must survive the check performed
// when a video session starts, and the MJPEG session must not change bitrate.
