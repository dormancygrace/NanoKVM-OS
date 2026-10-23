package auth

import (
	"bufio"
	"fmt"
	"net"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"NanoKVM-Server/authn"

	"github.com/gin-gonic/gin"
)

// A client that never finishes its login body is cut off: the connection
// must not stay open (with its buffers) for as long as the client likes.
func TestUnfinishedLoginBodyTimesOut(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useLoginProtectionConfig(t)
	useLoginAttemptRecords(t, map[string]*loginAttempt{})
	t.Cleanup(useTestStore(authn.NewStore(filepath.Join(t.TempDir(), "pwd"))))
	previous := loginBodyTimeout
	loginBodyTimeout = 200 * time.Millisecond
	t.Cleanup(func() { loginBodyTimeout = previous })

	router := gin.New()
	router.POST("/login", NewService().Login)
	server := httptest.NewServer(router)
	defer server.Close()

	conn, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// 100 bytes announced, 10 sent, the rest never.
	fmt.Fprintf(conn, "POST /login HTTP/1.1\r\nHost: kvm\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{\"username\"")
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	start := time.Now()
	_, err = bufio.NewReader(conn).ReadString('\n')
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Fatalf("connection still open after %v (read: %v)", elapsed, err)
	}
}
