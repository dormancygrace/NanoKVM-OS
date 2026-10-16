package router

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"NanoKVM-Server/middleware"
	"github.com/gin-gonic/gin"
)

func TestUserDiskStreamingRouteKeepsItsOwnBodyLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.LimitRequestBody(ownBodyLimitRoutes...))
	var readErr error
	var count int64
	r.POST("/api/storage/user-disk/files/upload", func(c *gin.Context) {
		count, readErr = io.Copy(io.Discard, c.Request.Body)
	})
	body := bytes.Repeat([]byte("a"), int(middleware.DefaultBodyLimit)+1)
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/storage/user-disk/files/upload", bytes.NewReader(body)))
	if readErr != nil || count != int64(len(body)) {
		t.Fatalf("stream was capped at the JSON limit: %d %v", count, readErr)
	}
}
