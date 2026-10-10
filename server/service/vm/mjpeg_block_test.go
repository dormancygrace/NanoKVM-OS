package vm

import (
	"NanoKVM-Server/authn"
	"NanoKVM-Server/common"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
)

// fakeCapture reports a capture size and a stream limit, and keeps the saved
// type and stream limit files in a temporary directory.
func fakeCapture(t *testing.T, width, height, limit int) {
	t.Helper()
	oldInput, oldMonitor, previous := captureInput, applyVideoMonitorSettings, common.GetScreen().Height
	oldFiles := map[string]string{}
	for _, key := range []string{"type", "resolution"} {
		oldFiles[key] = screenFileMap[key]
		screenFileMap[key] = filepath.Join(t.TempDir(), key)
	}
	captureInput = func() (int, int) { return width, height }
	applyVideoMonitorSettings = func(common.MonitorSettings) error { return nil }
	common.SetScreen("resolution", limit)
	t.Cleanup(func() {
		captureInput, applyVideoMonitorSettings = oldInput, oldMonitor
		for key, path := range oldFiles {
			screenFileMap[key] = path
		}
		common.SetScreen("resolution", int(previous))
	})
}

func setStreamType(t *testing.T, value int) (code int, msg string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/", bytes.NewBufferString(fmt.Sprintf(`{"type":"type","value":%d}`, value)))
	c.Request.Header.Set("Content-Type", "application/json")
	setScreenAs(c, authn.RoleAdmin)
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("response %q: %v", recorder.Body.String(), err)
	}
	return result.Code, result.Msg
}

func TestScreenRefusesMjpegAt4K(t *testing.T) {
	fakeCapture(t, 3840, 2160, 0)
	code, msg := setStreamType(t, 0)
	if code == 0 || msg != common.MjpegBlockedMessage {
		t.Fatalf("MJPEG at 3840x2160: code %d, %q", code, msg)
	}
	if _, err := os.Stat(screenFileMap["type"]); !os.IsNotExist(err) {
		t.Fatal("a refused type was saved")
	}
	for _, codec := range []int{1, 2} {
		if code, msg := setStreamType(t, codec); code != 0 {
			t.Fatalf("codec %d refused: %d %q", codec, code, msg)
		}
	}

	// The stream limit does not help: a limited 4K input produces no JPEG frames.
	common.SetScreen("resolution", 1440)
	if code, _ := setStreamType(t, 0); code == 0 {
		t.Fatal("MJPEG accepted at 3840x2160 with the stream limited to 1440p")
	}

	fakeCapture(t, 2560, 1440, 0)
	if code, msg := setStreamType(t, 0); code != 0 {
		t.Fatalf("MJPEG at 2560x1440: %d %q", code, msg)
	}
}

func TestVideoSettingsRefuseMjpegByTheResultingInput(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		width, height, limit int
		body                 string
		allowed              bool
	}{
		{"4K input", 3840, 2160, 0, `{"type":0}`, false},
		{"4K input, 2160 limit", 3840, 2160, 2160, `{"type":0,"quality":80}`, false},
		{"limit set with it", 3840, 2160, 0, `{"type":0,"height":1440}`, false},
		{"limit lifted with it", 3840, 2160, 1440, `{"type":0,"height":0}`, false},
		{"4K input, 1440p limit", 3840, 2160, 1440, `{"type":0}`, false},
		{"1440p input, limit set with it", 2560, 1440, 0, `{"type":0,"height":1080}`, true},
		{"4K monitor set with it", 1920, 1080, 0, `{"type":0,"monitor":2160}`, false},
		{"4K monitor set with it, limited", 1920, 1080, 0, `{"type":0,"monitor":2160,"height":1080}`, false},
		{"1080p monitor set with it", 3840, 2160, 1080, `{"type":0,"monitor":1080}`, true},
		{"1080p", 1920, 1080, 0, `{"type":0}`, true},
		{"no signal", 0, 0, 0, `{"type":0}`, true},
		{"H.265 at 4K", 3840, 2160, 0, `{"type":2}`, true},
		{"H.264 at 4K, limited", 3840, 2160, 1440, `{"type":1,"height":1440}`, true},
	} {
		fakeCapture(t, tc.width, tc.height, tc.limit)
		code, raw := setVideoAs(t, tc.body, authn.RoleAdmin)
		if (code == 0) != tc.allowed {
			t.Errorf("%s: %s: code %d, allowed %v: %s", tc.name, tc.body, code, tc.allowed, raw)
		}
		if !tc.allowed {
			for _, key := range []string{"type", "resolution"} {
				if _, err := os.Stat(screenFileMap[key]); !os.IsNotExist(err) {
					t.Errorf("%s: a refused request wrote %s", tc.name, key)
				}
			}
		}
	}
}

func TestVideoCapabilitiesMarkMjpegUnavailableAt4K(t *testing.T) {
	mjpeg := func() mjpegCapability {
		return videoCapabilities()["stream"].(gin.H)["mjpeg"].(mjpegCapability)
	}
	fakeCapture(t, 3840, 2160, 0)
	if got := mjpeg(); got.Available || got.Reason != "mjpeg-4k" || got.MaxSide != 2560 {
		t.Fatalf("4K: %+v", got)
	}
	common.SetScreen("resolution", 1440)
	if got := mjpeg(); got.Available || got.Reason != "mjpeg-4k" {
		t.Fatalf("4K limited to 1440p: %+v", got)
	}
	fakeCapture(t, 2560, 1440, 0)
	if got := mjpeg(); !got.Available || got.Reason != "" {
		t.Fatalf("1440p: %+v", got)
	}
}
