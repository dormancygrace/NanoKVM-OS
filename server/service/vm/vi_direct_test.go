package vm

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// GET /api/vm/screen names the codecs that "straight from capture" applies to.
func TestScreenReportsDirectCaptureCodecs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldHevc, oldH264 := hevcDirectSupported, viDirectH264Supported
	t.Cleanup(func() { hevcDirectSupported, viDirectH264Supported = oldHevc, oldH264 })
	for _, c := range []struct{ hevc, h264 bool }{{false, false}, {true, false}, {false, true}, {true, true}} {
		hevcDirectSupported = func() bool { return c.hevc }
		viDirectH264Supported = func() bool { return c.h264 }
		r := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(r)
		(&Service{}).GetScreen(ctx)
		var got struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(r.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Data["hevcDirectSupported"] != c.hevc || got.Data["viDirectH264Supported"] != c.h264 {
			t.Fatalf("%+v: %s", c, r.Body.String())
		}
		if _, ok := got.Data["hevcDirect"].(float64); !ok {
			t.Fatalf("the setting stays under its API name: %s", r.Body.String())
		}
	}
}
