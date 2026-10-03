package stream

import (
	"NanoKVM-Server/authn"
	"NanoKVM-Server/middleware"
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestEncoderSelectionRequiresExplicitAuthorizedValidRequest(t *testing.T) {
	oldFile, oldSource := encoderSelectionFile, defaultVideoSource
	encoderSelectionFile = filepath.Join(t.TempDir(), "encoder_codec")
	defaultVideoSource = newVideoSource(func(EncoderConfig) ([]byte, []byte, int) { return nil, nil, 0 })
	defer func() { encoderSelectionFile = oldFile; defaultVideoSource = oldSource }()
	for _, tc := range []struct {
		role    authn.Role
		body    string
		status  int
		success bool
	}{
		{authn.RoleUser, `{"codec":"h265"}`, 403, false},
		{authn.RoleAdmin, `{"codec":"invalid"}`, 200, false},
		{authn.RoleAdmin, `{"codec":"h265"}`, 200, true},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("principal", middleware.Principal{Username: "tester", Role: tc.role})
		c.Request = httptest.NewRequest("POST", "/api/stream/state", bytes.NewBufferString(tc.body))
		c.Request.Header.Set("Content-Type", "application/json")
		SetEncoderState(c)
		if w.Code != tc.status {
			t.Fatalf("status %d, want %d", w.Code, tc.status)
		}
		if w.Code == 200 {
			var response struct {
				Code int `json:"code"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if (response.Code == 0) != tc.success {
				t.Fatalf("response %s", w.Body.String())
			}
		}
		_, selected := defaultVideoSource.selectedConfig()
		if selected != tc.success {
			t.Fatal("failed request changed encoder selection")
		}
	}
	data, err := os.ReadFile(encoderSelectionFile)
	if err != nil || string(data) != "h265\n" {
		t.Fatalf("selection not persisted: %q %v", data, err)
	}
}

func TestEncoderSelectionWriteFailureKeepsActiveCodec(t *testing.T) {
	oldFile, oldSource := encoderSelectionFile, defaultVideoSource
	encoderSelectionFile = filepath.Join(t.TempDir(), "missing", "encoder_codec")
	defaultVideoSource = newVideoSource(func(EncoderConfig) ([]byte, []byte, int) { return nil, nil, 0 })
	defer func() { encoderSelectionFile = oldFile; defaultVideoSource = oldSource }()
	old, _ := defaultVideoSource.subscribe(LegacyEncoderConfig())
	defer old.Close()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("principal", middleware.Principal{Username: "tester", Role: authn.RoleAdmin})
	c.Request = httptest.NewRequest("POST", "/api/stream/state", bytes.NewBufferString(`{"codec":"h265"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	SetEncoderState(c)
	select {
	case <-old.done:
		t.Fatal("failed save interrupted existing viewer")
	default:
	}
	if _, selected := defaultVideoSource.selectedConfig(); selected {
		t.Fatal("failed save published codec")
	}
}
