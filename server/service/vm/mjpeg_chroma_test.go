package vm

import (
	"NanoKVM-Server/common"
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMjpegChromaApplyPersistAndFallback(t *testing.T) {
	oldFile, oldApply, oldRead := screenFileMap["mjpeg_chroma"], applyMjpegChroma, readMjpegChromaStatus
	oldChroma := common.GetScreen().MjpegChroma
	defer func() {
		screenFileMap["mjpeg_chroma"] = oldFile
		applyMjpegChroma = oldApply
		readMjpegChromaStatus = oldRead
		common.SetScreen("mjpeg_chroma", int(oldChroma))
	}()
	file := filepath.Join(t.TempDir(), "chroma")
	screenFileMap["mjpeg_chroma"] = file
	common.SetScreen("mjpeg_chroma", 420)
	var calls []uint16
	applyMjpegChroma = func(v uint16) int { calls = append(calls, v); return 0 }
	readMjpegChromaStatus = func() (uint16, string) { return 420, "video" }
	type response struct {
		Code int
		Data struct {
			Selected uint16 `json:"mjpegChroma"`
			Active   uint16 `json:"mjpegChromaActive"`
			Reason   string `json:"mjpegChromaFallback"`
		}
	}
	request := func(value int) response {
		t.Helper()
		r := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(r)
		c.Request = httptest.NewRequest("POST", "/", bytes.NewBufferString(fmt.Sprintf(`{"type":"mjpeg_chroma","value":%d}`, value)))
		c.Request.Header.Set("Content-Type", "application/json")
		(&Service{}).SetScreen(c)
		var result response
		if err := json.Unmarshal(r.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	for _, v := range []int{-1, 0, 421, 65536} {
		if r := request(v); r.Code == 0 {
			t.Fatalf("accepted %d", v)
		}
	}
	if len(calls) != 0 {
		t.Fatal("invalid input reached native")
	}
	for _, v := range []int{422, 422, 420} {
		r := request(v)
		if r.Code != 0 || r.Data.Selected != uint16(v) || r.Data.Active != 420 || r.Data.Reason != "video" {
			t.Fatalf("response %+v", r)
		}
		data, err := os.ReadFile(file)
		if err != nil || string(data) != fmt.Sprint(v) || common.GetScreen().MjpegChroma != uint16(v) {
			t.Fatalf("persistence %q %v", data, err)
		}
	}
	if !reflect.DeepEqual(calls, []uint16{422, 422, 420}) {
		t.Fatalf("explicit retry must reach native: %v", calls)
	}
	screenFileMap["mjpeg_chroma"] = filepath.Join(t.TempDir(), "missing", "chroma")
	calls = nil
	if r := request(422); r.Code != -2 {
		t.Fatalf("write failure %+v", r)
	}
	if !reflect.DeepEqual(calls, []uint16{422, 420}) || common.GetScreen().MjpegChroma != 420 {
		t.Fatalf("failed write did not restore intent: %v", calls)
	}
	screenFileMap["mjpeg_chroma"] = file
	applyMjpegChroma = func(v uint16) int { return -1 }
	if r := request(422); r.Code != -4 {
		t.Fatalf("native failure %+v", r)
	}
	data, _ := os.ReadFile(file)
	if string(data) != "420" {
		t.Fatal("native failure was persisted")
	}
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	(&Service{}).GetScreen(c)
	var got response
	if err := json.Unmarshal(r.Body.Bytes(), &got); err != nil || got.Data.Active != 420 || got.Data.Reason != "video" {
		t.Fatalf("GET %s %v", r.Body.String(), err)
	}
}
