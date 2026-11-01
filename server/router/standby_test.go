package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"NanoKVM-Server/standby"
	"github.com/gin-gonic/gin"
)

func TestStandbyAdmissionKeepsReadsAndBlocksNewStreams(t *testing.T) {
	standby.Requests.Cancel()
	t.Cleanup(standby.Requests.Cancel)
	r := gin.New()
	r.Use(standbyAdmission())
	r.NoRoute(func(c *gin.Context) { c.Status(http.StatusOK) })
	if err := standby.Requests.Begin(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path, upgrade string
		code                  int
	}{
		{"GET", "/assets/index.js", "", 200},
		{"HEAD", "/assets/index.js", "", 200},
		{"OPTIONS", "/api/vm/info", "", 200},
		{"GET", "/api/vm/info", "", 200},
		{"POST", "/api/vm/screen", "", 409},
		{"GET", "/api/vm/terminal", "websocket", 409},
		{"GET", "/api/vm/terminal", "h2c, WebSocket", 409},
		{"GET", "/api/stream/mjpeg", "", 409},
		{"GET", "/api/stream/state", "", 200},
		{"GET", "/api/network/wifi/scan", "", 409},
		{"GET", "/api/storage/user-disk/files", "", 409},
		{"GET", "/api/storage/user-disk/files/download", "", 409},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		if tc.upgrade != "" {
			req.Header.Set("Connection", "keep-alive, Upgrade")
			req.Header.Set("Upgrade", tc.upgrade)
		}
		rsp := httptest.NewRecorder()
		r.ServeHTTP(rsp, req)
		if rsp.Code != tc.code {
			t.Fatal(tc, rsp.Code)
		}
	}
}
