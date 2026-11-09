package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/middleware"

	"github.com/gin-gonic/gin"
)

type totpEnv struct {
	t      *testing.T
	store  *authn.Store
	router *gin.Engine
	clock  time.Time
}

func newTOTPEnv(t *testing.T) *totpEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store := authn.NewStore(filepath.Join(t.TempDir(), "pwd"))
	t.Cleanup(useTestStore(store))
	useLoginAttemptRecords(t, map[string]*loginAttempt{})
	previousPenalty, previousNow := loginPenalty, now
	loginPenalty = time.Millisecond
	env := &totpEnv{t: t, store: store, clock: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)}
	now = func() time.Time { return env.clock }
	t.Cleanup(func() { loginPenalty, now = previousPenalty, previousNow })

	service := NewService()
	router := gin.New()
	router.POST("/login", service.Login)
	router.POST("/login/totp", service.LoginTOTP)
	api := router.Group("/").Use(middleware.CheckToken())
	api.GET("/account", service.GetAccount)
	api.POST("/totp/setup", service.TOTPSetup)
	api.POST("/totp/enable", service.TOTPEnable)
	api.POST("/totp/disable", service.TOTPDisable)
	api.POST("/totp/recovery-codes", service.TOTPRecoveryCodes)
	admin := router.Group("/").Use(middleware.CheckToken(), middleware.RequireRole(authn.RoleAdmin))
	admin.DELETE("/users/:username/totp", service.ResetUserTOTP)
	env.router = router

	changeFactoryPassword(t, store)
	if err := store.Create("alice", "alice-password", authn.RoleUser); err != nil {
		t.Fatal(err)
	}
	if err := store.Create("bob", "bob-password", authn.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	return env
}

type apiResponse struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func (e *totpEnv) call(method, path string, body any, cookie *http.Cookie, remote string) (apiResponse, *httptest.ResponseRecorder) {
	e.t.Helper()
	recorder := httptest.NewRecorder()
	request := jsonRequest(method, path, body)
	if remote != "" {
		request.RemoteAddr = remote
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	e.router.ServeHTTP(recorder, request)
	var rsp apiResponse
	if recorder.Code == http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &rsp); err != nil {
			e.t.Fatalf("%s %s: %s", method, path, recorder.Body.String())
		}
	} else {
		rsp.Code = -recorder.Code
	}
	return rsp, recorder
}

func sessionOf(recorder *httptest.ResponseRecorder) *http.Cookie {
	for _, cookie := range recorder.Result().Cookies() {
		if (cookie.Name == middleware.CookieName || cookie.Name == middleware.HTTPCookieName) && cookie.Value != "" {
			return cookie
		}
	}
	return nil
}

func (e *totpEnv) code(secret string) string {
	e.t.Helper()
	code, err := authn.TOTPCode(secret, e.clock)
	if err != nil {
		e.t.Fatal(err)
	}
	return code
}

// tick moves the clock to the next code.
func (e *totpEnv) tick() { e.clock = e.clock.Add(30 * time.Second) }

// enable turns TOTP on for a signed-in user and returns the secret and codes.
func (e *totpEnv) enable(cookie *http.Cookie) (string, []string) {
	e.t.Helper()
	rsp, recorder := e.call(http.MethodPost, "/totp/setup", nil, cookie, "")
	var setup struct{ Secret, URI string }
	if rsp.Code != 0 || json.Unmarshal(rsp.Data, &setup) != nil || setup.Secret == "" {
		e.t.Fatalf("setup: %+v", rsp)
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		e.t.Fatal("the secret may be cached")
	}
	if rsp, _ = e.call(http.MethodPost, "/totp/enable", map[string]any{"otp": "000000"}, cookie, ""); rsp.Code == 0 {
		e.t.Fatal("a wrong confirmation code enabled TOTP")
	}
	rsp, _ = e.call(http.MethodPost, "/totp/enable", map[string]any{"otp": e.code(setup.Secret)}, cookie, "")
	var codes struct{ RecoveryCodes []string }
	if rsp.Code != 0 || json.Unmarshal(rsp.Data, &codes) != nil || len(codes.RecoveryCodes) != authn.RecoveryCodeCount {
		e.t.Fatalf("enable: %+v", rsp)
	}
	return setup.Secret, codes.RecoveryCodes
}

func (e *totpEnv) password(username, password string, extra map[string]any) (apiResponse, *httptest.ResponseRecorder) {
	body := map[string]any{"username": username, "password": encryptForRequest(e.t, password)}
	for key, value := range extra {
		body[key] = value
	}
	return e.call(http.MethodPost, "/login", body, nil, "")
}

func TestTOTPLoginWithChallenge(t *testing.T) {
	e := newTOTPEnv(t)
	_, recorder := e.password("alice", "alice-password", nil)
	cookie := sessionOf(recorder)
	secret, _ := e.enable(cookie)

	var account struct {
		TOTPEnabled       bool
		RecoveryCodesLeft int
	}
	rsp, _ := e.call(http.MethodGet, "/account", nil, cookie, "")
	if json.Unmarshal(rsp.Data, &account) != nil || !account.TOTPEnabled || account.RecoveryCodesLeft != 10 {
		t.Fatalf("account: %s", rsp.Data)
	}

	// A wrong password says nothing about the second factor.
	if rsp, _ = e.password("alice", "wrong-password", nil); rsp.Code != -2 {
		t.Fatalf("wrong password: %+v", rsp)
	}
	// The right password asks for a code and sets no session.
	rsp, recorder = e.password("alice", "alice-password", nil)
	var challenge struct{ Challenge string }
	if rsp.Code != codeTOTPRequired || json.Unmarshal(rsp.Data, &challenge) != nil || len(challenge.Challenge) != 64 || sessionOf(recorder) != nil {
		t.Fatalf("challenge: %+v", rsp)
	}
	// The code confirming the setup is spent; the next one signs in.
	if rsp, _ = e.call(http.MethodPost, "/login/totp", map[string]any{"challenge": challenge.Challenge, "otp": e.code(secret)}, nil, ""); rsp.Code != -2 {
		t.Fatalf("setup code reused: %+v", rsp)
	}
	// Not from another address.
	e.tick()
	if rsp, _ = e.call(http.MethodPost, "/login/totp", map[string]any{"challenge": challenge.Challenge, "otp": e.code(secret)}, nil, "198.51.100.7:1234"); rsp.Code != -8 {
		t.Fatalf("challenge from another address: %+v", rsp)
	}
	rsp, recorder = e.call(http.MethodPost, "/login/totp", map[string]any{"challenge": challenge.Challenge, "otp": e.code(secret)}, nil, "")
	if rsp.Code != 0 || sessionOf(recorder) == nil {
		t.Fatalf("second factor: %+v", rsp)
	}
	// The challenge is spent.
	if rsp, _ = e.call(http.MethodPost, "/login/totp", map[string]any{"challenge": challenge.Challenge, "otp": e.code(secret)}, nil, ""); rsp.Code != -8 {
		t.Fatalf("challenge reused: %+v", rsp)
	}
}

// the lockout is tested elsewhere

// It expires.

func TestTOTPOneRequestLoginAndRecoveryCode(t *testing.T) {
	e := newTOTPEnv(t)
	_, recorder := e.password("alice", "alice-password", nil)
	secret, codes := e.enable(sessionOf(recorder))
	e.tick()
	// Scripts send the code with the password.
	rsp, recorder := e.password("alice", "alice-password", map[string]any{"otp": e.code(secret)})
	if rsp.Code != 0 || sessionOf(recorder) == nil {
		t.Fatalf("one-request login: %+v", rsp)
	}
	if rsp, _ = e.password("alice", "alice-password", map[string]any{"otp": e.code(secret)}); rsp.Code != -2 {
		t.Fatalf("a code was accepted twice: %+v", rsp)
	}
	if rsp, recorder = e.password("alice", "alice-password", map[string]any{"otp": codes[0]}); rsp.Code != 0 || sessionOf(recorder) == nil {
		t.Fatalf("recovery code: %+v", rsp)
	}
	if rsp, _ = e.password("alice", "alice-password", map[string]any{"otp": codes[0]}); rsp.Code != -2 {
		t.Fatalf("a recovery code was accepted twice: %+v", rsp)
	}
}

func TestTOTPWrongCodesCountTowardLockout(t *testing.T) {
	e := newTOTPEnv(t)
	_, recorder := e.password("alice", "alice-password", nil)
	e.enable(sessionOf(recorder))
	locked := false
	for range 30 {
		rsp, _ := e.password("alice", "alice-password", map[string]any{"otp": "000000"})
		if rsp.Code != -2 {
			locked = true
			break
		}
	}
	if !locked {
		t.Fatal("wrong codes never locked the login")
	}
}

// A correct password must not clear the failures of earlier challenges:
// otherwise each new challenge would buy five more guesses.

// locked

func TestTOTPDisableAndRecoveryCodesNeedPasswordAndCode(t *testing.T) {
	e := newTOTPEnv(t)
	_, recorder := e.password("alice", "alice-password", nil)
	cookie := sessionOf(recorder)
	secret, codes := e.enable(cookie)
	e.tick()
	if rsp, _ := e.call(http.MethodPost, "/totp/recovery-codes", map[string]any{"password": encryptForRequest(t, "wrong-password"), "otp": e.code(secret)}, cookie, ""); rsp.Code == 0 {
		t.Fatal("new recovery codes without the password")
	}
	rsp, _ := e.call(http.MethodPost, "/totp/recovery-codes", map[string]any{"password": encryptForRequest(t, "alice-password"), "otp": e.code(secret)}, cookie, "")
	var fresh struct{ RecoveryCodes []string }
	if rsp.Code != 0 || json.Unmarshal(rsp.Data, &fresh) != nil || len(fresh.RecoveryCodes) != 10 {
		t.Fatalf("recovery codes: %+v", rsp)
	}
	if rsp, _ = e.call(http.MethodPost, "/totp/disable", map[string]any{"password": encryptForRequest(t, "alice-password"), "otp": codes[1]}, cookie, ""); rsp.Code == 0 {
		t.Fatal("an old recovery code still works")
	}
	if rsp, _ = e.call(http.MethodPost, "/totp/disable", map[string]any{"password": encryptForRequest(t, "alice-password"), "otp": fresh.RecoveryCodes[0]}, cookie, ""); rsp.Code != 0 {
		t.Fatalf("disable: %+v", rsp)
	}
	if rsp, recorder = e.password("alice", "alice-password", nil); rsp.Code != 0 || sessionOf(recorder) == nil {
		t.Fatalf("login after disable: %+v", rsp)
	}
}

func TestAdministratorResetsTOTP(t *testing.T) {
	e := newTOTPEnv(t)
	_, recorder := e.password("alice", "alice-password", nil)
	e.enable(sessionOf(recorder))
	_, recorder = e.password("admin", ownerPassword, nil)
	owner := sessionOf(recorder)
	_, recorder = e.password("bob", "bob-password", nil)
	bob := sessionOf(recorder)
	e.tick()
	e.enable(owner)

	if rsp, _ := e.call(http.MethodDelete, "/users/admin/totp", nil, bob, ""); rsp.Code != -3 {
		t.Fatalf("another admin reset the device owner: %+v", rsp)
	}
	if rsp, _ := e.call(http.MethodDelete, "/users/admin/totp", nil, owner, ""); rsp.Code != -1 {
		t.Fatalf("self reset: %+v", rsp)
	}
	if rsp, _ := e.call(http.MethodDelete, "/users/alice/totp", nil, bob, ""); rsp.Code != 0 {
		t.Fatalf("reset: %+v", rsp)
	}
	if rsp, recorder := e.password("alice", "alice-password", nil); rsp.Code != 0 || sessionOf(recorder) == nil {
		t.Fatalf("login after reset: %+v", rsp)
	}
	users, _ := e.store.List()
	for _, user := range users {
		if user.TOTPEnabled != (user.Username == "admin") {
			t.Fatalf("%+v", users)
		}
	}
}

// a device that booted without network

// Recovery codes do not need the clock.
