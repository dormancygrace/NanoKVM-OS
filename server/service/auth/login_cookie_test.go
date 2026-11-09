package auth

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/config"
	"NanoKVM-Server/middleware"

	"github.com/gin-gonic/gin"
)

func TestSessionCookieOnlyTrustsConfiguredProxy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	conf := config.GetInstance()
	originalTrustedProxies := conf.Security.TrustedProxies
	originalProto := conf.Proto
	defer func() {
		conf.Security.TrustedProxies = originalTrustedProxies
		conf.Proto = originalProto
	}()
	conf.Proto = "http"
	conf.Security.TrustedProxies = []string{"127.0.0.1"}

	for _, test := range []struct {
		name       string
		remoteAddr string
		secure     bool
	}{
		{name: "trusted proxy", remoteAddr: "127.0.0.1:1234", secure: true},
		{name: "untrusted peer", remoteAddr: "192.0.2.1:1234", secure: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			request := httptest.NewRequest(http.MethodPost, "http://upstream/login", nil)
			request.RemoteAddr = test.remoteAddr
			request.Header.Set("X-Forwarded-Proto", "https")
			context.Request = request
			setSessionCookie(context, "token")
			cookies := recorder.Result().Cookies()
			assertSessionCookieResponse(t, cookies, test.secure)
		})
	}
}

func TestSessionCookieForwardedProtoRejectsMultipleValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	conf := config.GetInstance()
	originalTrustedProxies := conf.Security.TrustedProxies
	originalProto := conf.Proto
	defer func() {
		conf.Security.TrustedProxies = originalTrustedProxies
		conf.Proto = originalProto
	}()
	conf.Proto = "http"
	conf.Security.TrustedProxies = []string{"::1/128"}

	for _, test := range []struct {
		name       string
		remoteAddr string
		xfp        string
		secure     bool
	}{
		{name: "trusted IPv6 proxy accepts a single forwarded scheme", remoteAddr: "[::1]:1234", xfp: "https", secure: true},
		{name: "trusted IPv6 proxy rejects multiple forwarded schemes", remoteAddr: "[::1]:1234", xfp: "https, http", secure: false},
		{name: "untrusted IPv6 peer cannot force secure cookie", remoteAddr: "[2001:db8::2]:1234", xfp: "https", secure: false},
		{name: "trusted proxy ignores invalid forwarded scheme", remoteAddr: "[::1]:1234", xfp: "ftp", secure: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			request := httptest.NewRequest(http.MethodPost, "http://upstream/login", nil)
			request.RemoteAddr = test.remoteAddr
			request.Header.Set("X-Forwarded-Proto", test.xfp)
			context.Request = request
			setSessionCookie(context, "token")
			cookies := recorder.Result().Cookies()
			assertSessionCookieResponse(t, cookies, test.secure)
		})
	}
}

func assertSessionCookieResponse(t *testing.T, cookies []*http.Cookie, secure bool) {
	t.Helper()
	wantCount := 2
	expiredName := "nano-kvm-token"
	if secure {
		expiredName = "nano-kvm-token-http"
	}
	if len(cookies) != wantCount {
		t.Fatalf("got %d Set-Cookie headers, want %d", len(cookies), wantCount)
	}
	active, expired := 0, 0
	for _, cookie := range cookies {
		if cookie.Name == expiredName {
			if cookie.MaxAge != -1 || cookie.Value != "" || cookie.Path != "/" || cookie.Domain != "" ||
				cookie.Secure != secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
				t.Fatalf("login must expire the other cookie name: %+v", cookie)
			}
			expired++
			continue
		}
		assertSessionCookieAttributes(t, cookie, secure)
		if cookie.Value == "" || cookie.MaxAge <= 0 {
			t.Fatalf("login did not issue a persistent session: %+v", cookie)
		}
		active++
	}
	if active != 1 || expired != wantCount-1 {
		t.Fatalf("cookie response has %d active and %d expired cookies", active, expired)
	}
}

func assertSessionCookieAttributes(t *testing.T, cookie *http.Cookie, secure bool) {
	t.Helper()
	name := "nano-kvm-token-http"
	if secure {
		name = "nano-kvm-token"
	}
	if cookie.Name != name || cookie.Secure != secure || !cookie.HttpOnly ||
		cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.Domain != "" {
		t.Fatalf("session cookie = %+v, want host-only %q, Secure=%v, HttpOnly, SameSite=Strict, Path=/", cookie, name, secure)
	}
}

// The Go jar does not enforce the browser's Secure-cookie overwrite rule.
// Different names are essential: the old Secure cookie must remain intact.

// A fresh HTTPS login removes the plain-HTTP credential while preserving
// the working Secure session. Use every Set-Cookie header, as a browser does.

// Revocation is disabled: deletion, not server-side revocation,
// must prevent the legacy fallback from reviving this browser login.

func TestHTTPLoginRemovesLegacyCookieForAnotherUser(t *testing.T) {
	store := authn.NewStore(filepath.Join(t.TempDir(), "pwd"))
	restore := useTestStore(store)
	defer restore()
	changeFactoryPassword(t, store)
	if err := store.Create("alice", "alice-password", authn.RoleUser); err != nil {
		t.Fatal(err)
	}
	conf := config.GetInstance()
	originalProto := conf.Proto
	t.Cleanup(func() { conf.Proto = originalProto })
	conf.Proto = "http"
	service := NewService()
	router := gin.New()
	router.POST("/login", service.Login)
	router.GET("/account", middleware.CheckToken(), service.GetAccount)
	legacy := loginCookie(t, router, "alice", "alice-password")
	legacy.Name = "nano-kvm-token"
	target, _ := url.Parse("http://kvm.example/account")
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(target, []*http.Cookie{legacy})
	recorder := requestJSONRecorder(router, http.MethodPost, "/login", map[string]any{
		"username": "admin", "password": encryptForRequest(t, ownerPassword),
	}, legacy)
	assertSessionCookieResponse(t, recorder.Result().Cookies(), false)
	jar.SetCookies(target, recorder.Result().Cookies())
	cookies := jar.Cookies(target)
	if len(cookies) != 1 || cookies[0].Name != "nano-kvm-token-http" {
		t.Fatalf("HTTP login retained the old identity: %v", cookies)
	}
	// Losing the new cookie must not reveal Alice's old session again.
	jar.SetCookies(target, []*http.Cookie{{Name: "nano-kvm-token-http", Path: "/", MaxAge: -1}})
	request := httptest.NewRequest(http.MethodGet, target.String(), nil)
	for _, cookie := range jar.Cookies(target) {
		request.AddCookie(cookie)
	}
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("legacy session restored after removing new cookie: %d", recorder.Code)
	}
	if status := requestJSON(router, http.MethodGet, "/account", nil, legacy); status != http.StatusOK {
		t.Fatalf("legacy session was revoked rather than removed: %d", status)
	}
}
