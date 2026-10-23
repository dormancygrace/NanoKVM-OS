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

// The exemption matches gin's route pattern. A request path such as
// /api/system/recovery/apply never matched the parameterised recovery
// route, so restores above the 1 MiB default failed.
func TestRecoveryArchiveRouteKeepsItsOwnBodyLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	routes := gin.New()
	recoveryRouter(routes)
	exempt := map[string]bool{}
	for _, path := range ownBodyLimitRoutes {
		exempt[path] = true
	}
	posts := 0
	for _, route := range routes.Routes() {
		if route.Method == http.MethodPost {
			posts++
			if !exempt[route.Path] {
				t.Errorf("recovery route %s is not exempt from the default body limit", route.Path)
			}
		}
	}
	if posts == 0 {
		t.Fatal("no recovery POST route registered")
	}

	r := gin.New()
	r.Use(middleware.LimitRequestBody(ownBodyLimitRoutes...))
	var count int64
	var readErr error
	r.POST("/api/system/recovery/:action", func(c *gin.Context) {
		count, readErr = io.Copy(io.Discard, c.Request.Body)
	})
	body := bytes.Repeat([]byte("a"), int(middleware.DefaultBodyLimit)+1)
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/system/recovery/apply", bytes.NewReader(body)))
	if readErr != nil || count != int64(len(body)) {
		t.Fatalf("archive capped at the default limit: %d %v", count, readErr)
	}
}
