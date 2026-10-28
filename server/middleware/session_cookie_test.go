package middleware

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/config"

	"github.com/gin-gonic/gin"
)

func TestSessionCookieAuthenticationAcrossProtocols(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := authn.NewStore(filepath.Join(t.TempDir(), "pwd"))
	admin, err := store.SetPassword("admin", "owner-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create("alice", "alice-password", authn.RoleUser); err != nil {
		t.Fatal(err)
	}
	alice, err := store.Get("alice")
	if err != nil {
		t.Fatal(err)
	}
	restore := useTestAuthStore(t, store)
	defer restore()
	conf := config.GetInstance()
	originalProto, originalProxies := conf.Proto, conf.Security.TrustedProxies
	t.Cleanup(func() { conf.Proto, conf.Security.TrustedProxies = originalProto, originalProxies })
	conf.Security.TrustedProxies = []string{"127.0.0.1/32"}
	adminToken, err := GenerateJWT(admin.Username, admin.TokenVersion)
	if err != nil {
		t.Fatal(err)
	}
	aliceToken, err := GenerateJWT(alice.Username, alice.TokenVersion)
	if err != nil {
		t.Fatal(err)
	}
	currentHTTP := &http.Cookie{Name: "nano-kvm-token-http", Value: adminToken}
	legacy := &http.Cookie{Name: "nano-kvm-token", Value: aliceToken}
	badHTTP := &http.Cookie{Name: "nano-kvm-token-http", Value: "invalid"}
	emptyHTTP := &http.Cookie{Name: "nano-kvm-token-http", Value: ""}
	badHTTPS := &http.Cookie{Name: "nano-kvm-token", Value: "invalid"}

	router := gin.New()
	router.GET("/account", CheckToken(), func(c *gin.Context) {
		principal, _ := CurrentPrincipal(c)
		c.String(http.StatusOK, principal.Username)
	})
	for _, test := range []struct {
		name, proto, url, remoteAddr, forwarded, bearer string
		cookies                                         []*http.Cookie
		wantUser                                        string
	}{
		{name: "new HTTP session", cookies: []*http.Cookie{currentHTTP}, wantUser: "admin"},
		{name: "legacy HTTP session", cookies: []*http.Cookie{legacy}, wantUser: "alice"},
		{name: "HTTP prefers new name regardless of header order", cookies: []*http.Cookie{legacy, currentHTTP}, wantUser: "admin"},
		{name: "invalid HTTP session cannot fall back", cookies: []*http.Cookie{badHTTP, legacy}},
		{name: "empty HTTP session cannot fall back", cookies: []*http.Cookie{emptyHTTP, legacy}},
		{name: "HTTPS keeps original name", proto: "https", cookies: []*http.Cookie{legacy}, wantUser: "alice"},
		{name: "HTTPS ignores HTTP session", proto: "https", cookies: []*http.Cookie{currentHTTP}},
		{name: "HTTPS prefers original name", proto: "https", cookies: []*http.Cookie{currentHTTP, legacy}, wantUser: "alice"},
		{name: "invalid HTTPS session cannot use HTTP session", proto: "https", cookies: []*http.Cookie{badHTTPS, currentHTTP}},
		{name: "actual TLS selects HTTPS", url: "https://kvm.example/account", cookies: []*http.Cookie{currentHTTP, legacy}, wantUser: "alice"},
		{name: "trusted TLS termination", remoteAddr: "127.0.0.1:1234", forwarded: "https", cookies: []*http.Cookie{currentHTTP, legacy}, wantUser: "alice"},
		{name: "untrusted peer cannot select HTTPS", forwarded: "https", cookies: []*http.Cookie{currentHTTP, legacy}, wantUser: "admin"},
		{name: "trusted HTTP forwarding overrides configured HTTPS", proto: "https", remoteAddr: "127.0.0.1:1234", forwarded: "http", cookies: []*http.Cookie{currentHTTP, legacy}, wantUser: "admin"},
		{name: "Bearer still takes precedence", bearer: "Bearer " + aliceToken, cookies: []*http.Cookie{currentHTTP}, wantUser: "alice"},
		{name: "invalid Bearer cannot use either cookie", bearer: "Bearer invalid", cookies: []*http.Cookie{currentHTTP, legacy}},
	} {
		t.Run(test.name, func(t *testing.T) {
			conf.Proto = test.proto
			if conf.Proto == "" {
				conf.Proto = "http"
			}
			url := test.url
			if url == "" {
				url = "http://kvm.example/account"
			}
			request := httptest.NewRequest(http.MethodGet, url, nil)
			request.RemoteAddr = test.remoteAddr
			if request.RemoteAddr == "" {
				request.RemoteAddr = "192.0.2.1:1234"
			}
			request.Header.Set("X-Forwarded-Proto", test.forwarded)
			request.Header.Set("Authorization", test.bearer)
			for _, cookie := range test.cookies {
				request.AddCookie(cookie)
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			if test.wantUser == "" {
				if recorder.Code != http.StatusUnauthorized {
					t.Fatalf("status = %d, want 401", recorder.Code)
				}
			} else if recorder.Code != http.StatusOK || recorder.Body.String() != test.wantUser {
				t.Fatalf("response = %d %q, want 200 %q", recorder.Code, recorder.Body.String(), test.wantUser)
			}
		})
	}
}
