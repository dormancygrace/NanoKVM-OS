package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestLimitRequestBodyCapsAllButOwnLimitRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(LimitRequestBody("/upload"))
	read := func(c *gin.Context) {
		n, err := io.Copy(io.Discard, c.Request.Body)
		if err != nil {
			c.String(http.StatusRequestEntityTooLarge, "%d", n)
			return
		}
		c.String(http.StatusOK, "%d", n)
	}
	router.POST("/login", read)
	router.POST("/upload", read)

	large := bytes.Repeat([]byte("a"), int(DefaultBodyLimit)+1)
	for path, want := range map[string]int{"/login": http.StatusRequestEntityTooLarge, "/upload": http.StatusOK} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(large)))
		if recorder.Code != want {
			t.Fatalf("%s = %d %s, want %d", path, recorder.Code, recorder.Body.String(), want)
		}
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/login", bytes.NewReader(large[:1024])))
	if recorder.Code != http.StatusOK {
		t.Fatalf("small body = %d", recorder.Code)
	}
}
