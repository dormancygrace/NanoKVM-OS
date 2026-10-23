package auth

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/config"
	"NanoKVM-Server/utils"

	"github.com/gin-gonic/gin"
)

func TestLoginBoundsRetainedUsername(t *testing.T) {
	useLoginProtectionConfig(t)
	useLoginAttemptRecords(t, make(map[string]*loginAttempt))
	gin.SetMode(gin.TestMode)
	t.Cleanup(useTestStore(authn.NewStore(filepath.Join(t.TempDir(), "pwd"))))
	conf := config.GetInstance()
	before := conf.Authentication
	conf.Authentication = "enable"
	t.Cleanup(func() { conf.Authentication = before })
	r := gin.New()
	r.POST("/login", NewService().Login)
	name := strings.Repeat("x", 512<<10)
	requestJSON(r, "POST", "/login", map[string]any{"username": name, "password": encryptForRequest(t, "wrong-password")}, nil)
	loginMutex.Lock()
	defer loginMutex.Unlock()
	a := loginAttempts["192.0.2.1"]
	if a == nil || a.failures != 1 {
		t.Fatalf("failure not counted: %+v", a)
	}
	if a.failedUser != "" || !a.otherFailures {
		t.Fatalf("retained %d bytes of attacker-supplied username after one failed HTTP login", len(a.failedUser))
	}
}

func TestLoginEarlyRepliesBoundRequestBody(t *testing.T) {
	for _, state := range []string{"locked", "authentication-disabled"} {
		t.Run(state, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			useLoginProtectionConfig(t)
			records := make(map[string]*loginAttempt)
			if state == "locked" {
				records["127.0.0.1"] = &loginAttempt{failures: 5, lastFailed: time.Now(), lockoutEnd: time.Now().Add(time.Minute)}
			}
			useLoginAttemptRecords(t, records)
			conf := config.GetInstance()
			oldAuth := conf.Authentication
			conf.Authentication = "enable"
			if state == "authentication-disabled" {
				conf.Authentication = "disable"
			}
			t.Cleanup(func() { conf.Authentication = oldAuth })
			old := loginBodyTimeout
			loginBodyTimeout = 200 * time.Millisecond
			t.Cleanup(func() { loginBodyTimeout = old })
			r := gin.New()
			r.POST("/login", NewService().Login)
			srv := httptest.NewServer(r)
			defer srv.Close()
			conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if _, err := fmt.Fprint(conn, "POST /login HTTP/1.1\r\nHost: kvm\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{\"username\""); err != nil {
				t.Fatal(err)
			}
			if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			line, err := bufio.NewReader(conn).ReadString('\n')
			if e, ok := err.(net.Error); ok && e.Timeout() {
				t.Fatalf("%s login with unfinished body still blocked after %v", state, time.Since(started))
			}
			if err != nil && err != io.EOF {
				t.Fatalf("unexpected response read error: %v", err)
			}
			if err == nil && !strings.HasPrefix(line, "HTTP/1.1 200 ") {
				t.Fatalf("unexpected response: %q", line)
			}
		})
	}
}

// A login deadline must not truncate a later upload on the same connection.
func TestLoginDeadlineDoesNotLeakToKeepAliveRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	conf := config.GetInstance()
	oldAuth, oldTimeout := conf.Authentication, loginBodyTimeout
	conf.Authentication = "disable"
	loginBodyTimeout = 200 * time.Millisecond
	t.Cleanup(func() {
		conf.Authentication, loginBodyTimeout = oldAuth, oldTimeout
	})
	r := gin.New()
	r.POST("/login", NewService().Login)
	r.POST("/upload", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.String(http.StatusBadRequest, "%v", err)
			return
		}
		c.String(http.StatusOK, "%s", body)
	})
	srv := httptest.NewUnstartedServer(r)
	srv.Config = utils.NewHTTPServer("", r, false)
	srv.Start()
	defer srv.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	readResponse := func(want string) {
		t.Helper()
		response, err := http.ReadResponse(reader, nil)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(body), want) {
			t.Fatalf("response status=%d body=%q err=%v; want %q", response.StatusCode, body, err, want)
		}
	}
	if _, err := io.WriteString(conn, "POST /login HTTP/1.1\r\nHost: kvm\r\nContent-Type: application/json\r\nContent-Length: 2\r\n\r\n{}"); err != nil {
		t.Fatal(err)
	}
	readResponse("success")
	if _, err := io.WriteString(conn, "POST /upload HTTP/1.1\r\nHost: kvm\r\nContent-Length: 4\r\n\r\nda"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * loginBodyTimeout)
	if _, err := io.WriteString(conn, "ta"); err != nil {
		t.Fatal(err)
	}
	readResponse("data")
}
