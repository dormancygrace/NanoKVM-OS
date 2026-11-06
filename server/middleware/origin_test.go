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
		// Some GET routes act: an image tag on another site must not call them.
		{http.MethodGet, "http://evil.example", "cross-site", http.StatusForbidden},
		{http.MethodGet, "", "cross-site", http.StatusForbidden},
		{http.MethodGet, "http://dashboard.local", "cross-site", http.StatusOK},
		{http.MethodGet, "", "same-origin", http.StatusOK},
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

	// A page outside the API, such as a link to the device, stays reachable.
	page := httptest.NewRequest(http.MethodGet, "http://kvm.local/", nil)
	page.Header.Set("Sec-Fetch-Site", "cross-site")
	router.GET("/", ok)
	navigation := httptest.NewRecorder()
	router.ServeHTTP(navigation, page)
	if navigation.Code != http.StatusOK {
		t.Fatalf("cross-site navigation = %d", navigation.Code)
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

// Without authentication, plain HTTP answers only the device's own names: a
// page whose DNS name was rebound to the device's address is refused.
func TestRequireDeviceHost(t *testing.T) {
	conf := config.GetInstance()
	originalProxies, originalOrigins := conf.Security.TrustedProxies, conf.Security.AllowedOrigins
	originalHostname := deviceHostname
	defer func() {
		conf.Security.TrustedProxies, conf.Security.AllowedOrigins = originalProxies, originalOrigins
		deviceHostname = originalHostname
	}()
	conf.Security.TrustedProxies = []string{"10.0.0.9"}
	conf.Security.AllowedOrigins = []string{"http://dashboard.example:3001"}
	deviceHostname = func() (string, error) { return "kvm-73d8", nil }

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequireDeviceHost())
	router.POST("/api/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	for _, test := range []struct {
		host, remote string
		want         int
	}{
		{"192.168.1.21", "192.168.1.5:5000", http.StatusOK},
		{"192.168.1.21:8080", "192.168.1.5:5000", http.StatusOK},
		{"[fe80::1]", "192.168.1.5:5000", http.StatusOK},
		{"localhost:80", "127.0.0.1:5000", http.StatusOK},
		{"kvm-73d8", "192.168.1.5:5000", http.StatusOK},
		{"KVM-73D8.local.", "192.168.1.5:5000", http.StatusOK},
		{"dashboard.example", "192.168.1.5:5000", http.StatusOK},
		{"evil.example", "192.168.1.5:5000", http.StatusMisdirectedRequest},
		{"kvm-73d8.evil.example", "192.168.1.5:5000", http.StatusMisdirectedRequest},
		{"192.168.1.21.nip.io", "192.168.1.5:5000", http.StatusMisdirectedRequest},
		{"kvm.example", "10.0.0.9:5000", http.StatusOK}, // trusted proxy
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/x", nil)
		request.Host, request.RemoteAddr = test.host, test.remote
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != test.want {
			t.Errorf("Host %q from %s = %d, want %d", test.host, test.remote, recorder.Code, test.want)
		}
	}
}
