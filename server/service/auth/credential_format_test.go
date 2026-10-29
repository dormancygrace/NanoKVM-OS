package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/proto"
	"github.com/gin-gonic/gin"
)

func TestPlainCredentialLifecycleAndLegacyLogin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := authn.NewStore(filepath.Join(t.TempDir(), "pwd"))
	restore := useTestStore(store)
	defer restore()
	changeFactoryPassword(t, store)
	service := NewService()
	router := gin.New()
	router.POST("/login", service.Login)
	authenticated := router.Group("/").Use(middleware.CheckToken())
	authenticated.GET("/account", service.GetAccount)
	authenticated.POST("/password", service.ChangePassword)
	admin := router.Group("/").Use(middleware.CheckToken(), middleware.RequireRole(authn.RoleAdmin))
	admin.POST("/users", service.CreateUser)
	admin.POST("/users/:username/password", service.ChangeUserPassword)
	adminCookie := loginCookie(t, router, "admin", ownerPassword)
	check := func(path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		recorder := requestJSONRecorder(router, http.MethodPost, path, body, cookie)
		var response proto.Response
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || recorder.Code != http.StatusOK || response.Code != 0 {
			t.Fatalf("%s failed: %d %s", path, recorder.Code, recorder.Body.String())
		}
		return recorder
	}
	const first = "päss+%20密碼"
	check("/users", map[string]any{"username": "alice", "password": first, "passwordEncoding": "plain", "role": "user"}, adminCookie)
	// A new-format account remains usable by a legacy client.
	oldCookie := loginCookie(t, router, "alice", first)
	const second = "U2FsdGVkX1-not-ciphertext"
	check("/users/alice/password", map[string]any{"password": second, "passwordEncoding": "plain"}, adminCookie)
	if got := requestJSON(router, http.MethodGet, "/account", nil, oldCookie); got != http.StatusUnauthorized {
		t.Fatalf("reset did not revoke session: %d", got)
	}
	login := check("/login", map[string]any{"username": "alice", "password": second, "passwordEncoding": "plain"}, nil)
	var cookie *http.Cookie
	for _, candidate := range login.Result().Cookies() {
		if candidate.Value != "" {
			cookie = candidate
			break
		}
	}
	if cookie == nil || !cookie.HttpOnly {
		t.Fatal("missing HttpOnly session cookie")
	}
	if strings.Contains(login.Body.String(), "token") {
		t.Fatal("browser received a bearer token")
	}
	const third = "new+%2B-password"
	check("/password", map[string]any{"currentPassword": second, "password": third, "passwordEncoding": "plain"}, cookie)
	if got := requestJSON(router, http.MethodGet, "/account", nil, cookie); got != http.StatusUnauthorized {
		t.Fatalf("password change did not revoke session: %d", got)
	}
	loginCookie(t, router, "alice", third)
}

func TestPlainLoginFormDoesNotUnescapePasswordTwice(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := authn.NewStore(filepath.Join(t.TempDir(), "pwd"))
	restore := useTestStore(store)
	defer restore()
	const password = "päss+%20密碼"
	if _, err := store.SetPassword("admin", password); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.POST("/login", NewService().Login)
	form := url.Values{"username": {"admin"}, "password": {password}, "passwordEncoding": {"plain"}}
	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if !hasSessionCookie(recorder.Result().Cookies()) {
		t.Fatalf("form login failed: %s", recorder.Body.String())
	}
}

func TestCredentialEncodingFailsClosedAndKeepsValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := authn.NewStore(filepath.Join(t.TempDir(), "pwd"))
	restore := useTestStore(store)
	defer restore()
	service := NewService()
	router := gin.New()
	router.POST("/users", service.CreateUser)
	for _, body := range []map[string]any{
		{"username": "alice", "role": "user", "password": "valid-password", "passwordEncoding": "unknown"},
		{"username": "alice", "role": "user", "password": "valid-password"},
		{"username": "alice", "role": "user", "password": "short", "passwordEncoding": "plain"},
	} {
		recorder := requestJSONRecorder(router, http.MethodPost, "/users", body, nil)
		var response proto.Response
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response.Code == 0 {
			t.Fatalf("invalid credential accepted: %s", recorder.Body.String())
		}
	}
	if _, err := store.Get("alice"); err == nil {
		t.Fatal("invalid request created an account")
	}
}
