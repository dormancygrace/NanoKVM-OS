package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"NanoKVM-Server/config"

	"github.com/gin-gonic/gin"
)

func TestRejectCrossOriginWrites(t *testing.T) {
	conf := config.GetInstance()
	originalProto, originalProxies, originalOrigins := conf.Proto, conf.Security.TrustedProxies, conf.Security.AllowedOrigins
	defer func() {
		conf.Proto, conf.Security.TrustedProxies, conf.Security.AllowedOrigins = originalProto, originalProxies, originalOrigins
	}()
	conf.Proto, conf.Security.TrustedProxies = "http", nil
	conf.Security.AllowedOrigins = []string{"http://dashboard.local"}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(AllowedOriginCORS(), RejectCrossOriginWrites())
	ok := func(c *gin.Context) { c.Status(http.StatusOK) }
	router.GET("/api/x", ok)
	router.POST("/api/x", ok)

	for _, test := range []struct {
		method, origin, fetchSite string
		want                      int
	}{
		{http.MethodPost, "http://kvm.local", "same-origin", http.StatusOK},
		{http.MethodPost, "", "", http.StatusOK}, // curl, automation
		{http.MethodPost, "http://dashboard.local", "cross-site", http.StatusOK},
		{http.MethodPost, "http://evil.example", "cross-site", http.StatusForbidden},
		{http.MethodPost, "null", "cross-site", http.StatusForbidden},
		{http.MethodPost, "", "cross-site", http.StatusForbidden},
		{http.MethodGet, "http://evil.example", "cross-site", http.StatusOK},
	} {
		request := httptest.NewRequest(test.method, "http://kvm.local/api/x", nil)
		request.Host = "kvm.local"
		if test.origin != "" {
			request.Header.Set("Origin", test.origin)
		}
		if test.fetchSite != "" {
			request.Header.Set("Sec-Fetch-Site", test.fetchSite)
		}
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != test.want {
			t.Fatalf("%s from %q (%s) = %d, want %d", test.method, test.origin, test.fetchSite, recorder.Code, test.want)
		}
	}

	// Cross-origin reads: only listed origins get the CORS answer.
	for origin, want := range map[string]string{"http://dashboard.local": "http://dashboard.local", "http://evil.example": ""} {
		request := httptest.NewRequest(http.MethodGet, "http://kvm.local/api/x", nil)
		request.Host = "kvm.local"
		request.Header.Set("Origin", origin)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != want {
			t.Fatalf("read from %s: Access-Control-Allow-Origin %q, want %q", origin, got, want)
		}
	}
}
