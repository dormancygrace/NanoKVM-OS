package auth

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"NanoKVM-Server/proto"

	"github.com/gin-gonic/gin"
)

// A one-request login whose password is replaced while it is checked must not
// get a session for the new account version, even with a valid code.
func TestOneRequestCodeAfterPasswordChange(t *testing.T) {
	e := newTOTPEnv(t)
	_, recorder := e.password("alice", "alice-password", nil)
	secret, codes := e.enable(sessionOf(recorder))
	e.tick()
	// The snapshot Authenticate returned before its bcrypt check ...
	snapshot, ok, err := e.store.Authenticate("alice", "alice-password")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	// ... then the password changes ...
	if _, err = e.store.SetPasswordAndRunIf("alice", "another-password", nil, nil); err != nil {
		t.Fatal(err)
	}
	// ... and the code is checked for the old snapshot.
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/login", nil)
	var rsp proto.Response
	if user := NewService().requireSecondFactor(c, "192.0.2.1", snapshot, e.code(secret), &rsp); user != nil {
		t.Fatalf("an old password proof signed in as version %d", user.TokenVersion)
	}
	// The code and the recovery codes were not used up.
	if rsp, recorder := e.password("alice", "another-password", map[string]any{"otp": e.code(secret)}); rsp.Code != 0 || sessionOf(recorder) == nil {
		t.Fatalf("login with the new password: %+v", rsp)
	}
	if rsp, _ := e.password("alice", "another-password", map[string]any{"otp": codes[0]}); rsp.Code != 0 {
		t.Fatalf("recovery code after the refusal: %+v", rsp)
	}
}

// The second login step has the same body deadline as the first: a client
// that never finishes its body is cut off, also when locked out.
func TestUnfinishedTOTPLoginBodyTimesOut(t *testing.T) {
	for _, state := range []string{"open", "locked"} {
		t.Run(state, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			useLoginProtectionConfig(t)
			records := map[string]*loginAttempt{}
			if state == "locked" {
				records["127.0.0.1"] = &loginAttempt{failures: 5, lastFailed: time.Now(), lockoutEnd: time.Now().Add(time.Minute)}
			}
			useLoginAttemptRecords(t, records)
			previous := loginBodyTimeout
			loginBodyTimeout = 200 * time.Millisecond
			t.Cleanup(func() { loginBodyTimeout = previous })

			router := gin.New()
			router.POST("/login/totp", NewService().LoginTOTP)
			server := httptest.NewServer(router)
			defer server.Close()
			conn, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			fmt.Fprint(conn, "POST /login/totp HTTP/1.1\r\nHost: kvm\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{\"challenge\"")
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			started := time.Now()
			_, err = bufio.NewReader(conn).ReadString('\n')
			if e, ok := err.(net.Error); ok && e.Timeout() {
				t.Fatalf("unfinished second-step body still blocked after %v", time.Since(started))
			}
			if err != nil && err != io.EOF {
				t.Fatalf("unexpected read error: %v", err)
			}
		})
	}
}
