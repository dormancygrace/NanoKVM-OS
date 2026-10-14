package auth

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/proto"

	"github.com/gin-gonic/gin"
)

type pausedLoginBody struct {
	io.Reader
	reading chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (b *pausedLoginBody) Read(p []byte) (int, error) {
	b.once.Do(func() {
		close(b.reading)
		<-b.resume
	})
	return b.Reader.Read(p)
}

// A slow request body must not reserve the password-check slot: the HTTP
// server deliberately has no global body-read deadline for streaming uploads.
func TestSlowLoginBodyDoesNotBlockAnotherLogin(t *testing.T) {
	checkPausedLoginBody(t, false)
}

func TestLoginRechecksLockoutAfterBodyRead(t *testing.T) {
	checkPausedLoginBody(t, true)
}

func checkPausedLoginBody(t *testing.T, lockWhileReading bool) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	useLoginProtectionConfig(t)
	useLoginAttemptRecords(t, map[string]*loginAttempt{})
	store := authn.NewStore(filepath.Join(t.TempDir(), "pwd"))
	t.Cleanup(useTestStore(store))
	changeFactoryPassword(t, store)
	previous := loginSlotWait
	loginSlotWait = 50 * time.Millisecond
	t.Cleanup(func() { loginSlotWait = previous })

	router := gin.New()
	router.POST("/login", NewService().Login)
	payload := map[string]string{"username": "admin", "password": encryptForRequest(t, ownerPassword)}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	body := &pausedLoginBody{Reader: bytes.NewReader(data), reading: make(chan struct{}), resume: make(chan struct{})}
	slow := httptest.NewRequest(http.MethodPost, "/login", body)
	slow.Header.Set("Content-Type", "application/json")
	slow.RemoteAddr = "192.0.2.90:1234"
	slowResponse := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		router.ServeHTTP(slowResponse, slow)
	}()
	resumeBody := sync.OnceFunc(func() { close(body.resume) })
	t.Cleanup(func() {
		resumeBody()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("slow request did not finish after its body was released")
		}
	})
	select {
	case <-body.reading:
	case <-time.After(5 * time.Second):
		t.Fatal("first login did not reach body parsing")
	}

	if lockWhileReading {
		for range 5 {
			RecordLoginFailure("192.0.2.90")
		}
		resumeBody()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("locked login did not finish after body parsing")
		}
		var result proto.Response
		if err := json.Unmarshal(slowResponse.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Code != -5 || hasSessionCookie(slowResponse.Result().Cookies()) {
			t.Fatalf("login bypassed a lockout recorded during body parsing: %+v", result)
		}
		return
	}

	ready := jsonRequest(http.MethodPost, "/login", payload)
	ready.RemoteAddr = "192.0.2.90:5678"
	response := httptest.NewRecorder()
	router.ServeHTTP(response, ready)
	var result proto.Response
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || !hasSessionCookie(response.Result().Cookies()) {
		t.Fatalf("complete login blocked by an unread body: %+v", result)
	}
}
