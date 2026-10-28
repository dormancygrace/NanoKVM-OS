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
	wantCount := 1
	if secure {
		wantCount = 2
	}
	if len(cookies) != wantCount {
		t.Fatalf("got %d Set-Cookie headers, want %d", len(cookies), wantCount)
	}
	active, expired := 0, 0
	for _, cookie := range cookies {
		if secure && cookie.Name == "nano-kvm-token-http" {
			if cookie.MaxAge != -1 || cookie.Value != "" || cookie.Path != "/" || cookie.Domain != "" ||
				!cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
				t.Fatalf("HTTPS login must expire the HTTP cookie: %+v", cookie)
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

func TestSessionLoginHTTPSHTTPAndBack(t *testing.T) {
	store := authn.NewStore(filepath.Join(t.TempDir(), "pwd"))
	restore := useTestStore(store)
	defer restore()
	changeFactoryPassword(t, store)
	conf := config.GetInstance()
	originalProto := conf.Proto
	t.Cleanup(func() { conf.Proto = originalProto })
	service := NewService()
	router := gin.New()
	router.POST("/login", service.Login)
	router.GET("/account", middleware.CheckToken(), service.GetAccount)

	conf.Proto = "https"
	httpsCookie := loginCookie(t, router, "admin", ownerPassword)
	assertSessionCookieAttributes(t, httpsCookie, true)
	httpsURL, _ := url.Parse("https://kvm.example/account")
	httpURL, _ := url.Parse("http://kvm.example/account")
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(httpsURL, []*http.Cookie{httpsCookie})
	if cookies := jar.Cookies(httpURL); len(cookies) != 0 {
		t.Fatalf("Secure session leaked to HTTP: %v", cookies)
	}

	conf.Proto = "http"
	httpCookie := loginCookie(t, router, "admin", ownerPassword)
	assertSessionCookieAttributes(t, httpCookie, false)
	// The Go jar does not enforce the browser's Secure-cookie overwrite rule.
	// Different names are essential: the old Secure cookie must remain intact.
	if httpCookie.Name == httpsCookie.Name {
		t.Fatal("HTTP login would collide with the old Secure cookie")
	}
	jar.SetCookies(httpURL, []*http.Cookie{httpCookie})
	if cookies := jar.Cookies(httpsURL); len(cookies) != 2 {
		t.Fatalf("expected both sessions to coexist, got %v", cookies)
	}
	for _, target := range []*url.URL{httpURL, httpsURL} {
		conf.Proto = target.Scheme
		request := httptest.NewRequest(http.MethodGet, target.String(), nil)
		for _, cookie := range jar.Cookies(target) {
			request.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("account after switching to %s = %d, want 200", target.Scheme, recorder.Code)
		}
	}
	// A fresh HTTPS login removes the plain-HTTP credential while preserving
	// the working Secure session. Use every Set-Cookie header, as a browser does.
	recorder := requestJSONRecorder(router, http.MethodPost, "/login", map[string]any{
		"username": "admin", "password": encryptForRequest(t, ownerPassword),
	}, nil)
	assertSessionCookieResponse(t, recorder.Result().Cookies(), true)
	jar.SetCookies(httpsURL, recorder.Result().Cookies())
	if cookies := jar.Cookies(httpURL); len(cookies) != 0 {
		t.Fatalf("HTTPS login retained a plain-HTTP credential: %v", cookies)
	}
	if cookies := jar.Cookies(httpsURL); len(cookies) != 1 || cookies[0].Name != "nano-kvm-token" {
		t.Fatalf("HTTPS login lost its Secure session: %v", cookies)
	}
}

func TestSessionLogoutClearsBothCookieNamesWithoutRevocation(t *testing.T) {
	store := authn.NewStore(filepath.Join(t.TempDir(), "pwd"))
	restore := useTestStore(store)
	defer restore()
	changeFactoryPassword(t, store)
	conf := config.GetInstance()
	originalProto := conf.Proto
	t.Cleanup(func() { conf.Proto = originalProto })
	conf.JWT.RevokeTokensOnLogout = false
	service := NewService()
	router := gin.New()
	router.POST("/login", service.Login)
	router.POST("/logout", middleware.CheckToken(), service.Logout)
	router.GET("/account", middleware.CheckToken(), service.GetAccount)
	for _, scheme := range []string{"http", "https"} {
		t.Run(scheme, func(t *testing.T) {
			conf.Proto = scheme
			current := loginCookie(t, router, "admin", ownerPassword)
			target, _ := url.Parse(scheme + "://kvm.example/logout")
			jar, err := cookiejar.New(nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"nano-kvm-token", "nano-kvm-token-http"} {
				cookie := *current
				cookie.Name = name
				cookie.Secure = name == "nano-kvm-token" && scheme == "https"
				jar.SetCookies(target, []*http.Cookie{&cookie})
			}
			request := httptest.NewRequest(http.MethodPost, target.String(), nil)
			for _, cookie := range jar.Cookies(target) {
				request.AddCookie(cookie)
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("logout = %d, want 200", recorder.Code)
			}
			cookies := recorder.Result().Cookies()
			if len(cookies) != 2 {
				t.Fatalf("logout must clear both names, got %v", cookies)
			}
			for _, cookie := range cookies {
				if cookie.MaxAge != -1 || cookie.Value != "" || cookie.Path != "/" ||
					cookie.Secure != (scheme == "https") || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
					t.Fatalf("invalid deletion cookie: %+v", cookie)
				}
			}
			jar.SetCookies(target, cookies)
			if cookies := jar.Cookies(target); len(cookies) != 0 {
				t.Fatalf("logout left cookies that could restore the session: %v", cookies)
			}
			// Revocation is disabled: deletion, not server-side revocation,
			// must prevent the legacy fallback from reviving this browser login.
			if status := requestJSON(router, http.MethodGet, "/account", nil, current); status != http.StatusOK {
				t.Fatalf("test token was unexpectedly revoked: %d", status)
			}
			if status := requestJSON(router, http.MethodGet, "/account", nil, nil); status != http.StatusUnauthorized {
				t.Fatalf("account after logout = %d, want 401", status)
			}
		})
	}
}
