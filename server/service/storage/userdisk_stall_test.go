package storage

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// stallServer serves the user disk file routes over HTTP/1.1, or over
// HTTP/2 with TLS as browsers reach the device.
func stallServer(t *testing.T, http2 bool) (files string, server *httptest.Server) {
	t.Helper()
	files = mockUserDiskMount(t)
	saved := userDiskStall
	userDiskStall = 500 * time.Millisecond
	t.Cleanup(func() { userDiskStall = saved })
	gin.SetMode(gin.TestMode)
	s := &Service{}
	r := gin.New()
	r.POST("/upload", s.UploadUserDiskFile)
	r.GET("/download", s.DownloadUserDiskFile)
	r.GET("/list", s.ListUserDiskFiles)
	server = httptest.NewUnstartedServer(r)
	if http2 {
		server.EnableHTTP2 = true
		server.StartTLS()
	} else {
		server.Start()
	}
	t.Cleanup(server.Close)
	return files, server
}

func diskHeld() bool {
	unlock, err := LockUserDiskForComposition()
	if err == nil {
		unlock()
	}
	return err != nil
}

// diskReleased waits until no file request holds the user disk. Poll only
// once a request is known to hold it: a poll racing the request's own TryLock
// would turn it away as busy.
func diskReleased(t *testing.T, within time.Duration) {
	t.Helper()
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if !diskHeld() {
			return
		}
	}
	t.Fatalf("the user disk was still held after %v", within)
}

// uploading waits for the upload's temporary file, which exists only while
// the request holds the disk.
func uploading(t *testing.T, files string) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if entries, _ := os.ReadDir(files); len(entries) == 1 && strings.HasPrefix(entries[0].Name(), ".upload-") {
			return
		}
	}
	t.Fatal("the upload did not start")
}

func noFilesLeft(t *testing.T, files string) {
	t.Helper()
	if entries, _ := os.ReadDir(files); len(entries) != 0 {
		t.Fatalf("a stalled upload left %v", entries)
	}
}

func TestUserDiskStalledUploadReleasesTheDisk(t *testing.T) {
	files, server := stallServer(t, false)
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "POST /upload?path=file HTTP/1.1\r\nHost: kvm\r\nContent-Length: 1000\r\n\r\npartial")
	uploading(t, files)
	diskReleased(t, 5*time.Second)
	noFilesLeft(t, files)
}

func TestUserDiskStalledUploadReleasesTheDiskOverHTTP2(t *testing.T) {
	files, server := stallServer(t, true)
	body, send := io.Pipe()
	defer send.Close()
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/upload?path=file", body)
	req.ContentLength = 1000
	go func() {
		if rsp, err := server.Client().Do(req); err == nil {
			rsp.Body.Close()
		}
	}()
	if _, err := send.Write([]byte("partial")); err != nil {
		t.Fatal(err)
	}
	uploading(t, files)
	diskReleased(t, 5*time.Second)
	noFilesLeft(t, files)
}

// slowBody sends one byte per tick: slower than the stall timeout overall,
// but never silent for that long.
type slowBody struct {
	left int
	tick time.Duration
}

func (b *slowBody) Read(p []byte) (int, error) {
	if b.left == 0 {
		return 0, io.EOF
	}
	time.Sleep(b.tick)
	b.left--
	p[0] = 'x'
	return 1, nil
}

func TestUserDiskSlowUploadIsNotCut(t *testing.T) {
	for _, http2 := range []bool{false, true} {
		files, server := stallServer(t, http2)
		body := &slowBody{left: 12, tick: userDiskStall / 5}
		req, _ := http.NewRequest(http.MethodPost, server.URL+"/upload?path=slow", body)
		req.ContentLength = 12
		rsp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(rsp.Body)
		rsp.Body.Close()
		if !strings.Contains(string(data), `"code":0`) || rsp.ProtoMajor != map[bool]int{false: 1, true: 2}[http2] {
			t.Fatalf("slow upload over HTTP/%d: %s", rsp.ProtoMajor, data)
		}
		if got, _ := os.ReadFile(filepath.Join(files, "slow")); string(got) != "xxxxxxxxxxxx" {
			t.Fatalf("slow upload stored %q", got)
		}
	}
}

// More than the socket buffers and the HTTP/2 flow-control window absorb.
func bigFile(t *testing.T, files string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(files, "big"), bytes.Repeat([]byte("x"), 32<<20), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestUserDiskStalledDownloadReleasesTheDisk(t *testing.T) {
	files, server := stallServer(t, false)
	bigFile(t, files)
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.(*net.TCPConn).SetReadBuffer(4096)
	fmt.Fprintf(conn, "GET /download?path=big HTTP/1.1\r\nHost: kvm\r\n\r\n")
	status, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || !strings.Contains(status, "200") {
		t.Fatalf("download: %q %v", status, err)
	}
	// The client stops reading, as a paused browser download does.
	diskReleased(t, 5*time.Second)
}

func TestUserDiskStalledDownloadReleasesTheDiskOverHTTP2(t *testing.T) {
	files, server := stallServer(t, true)
	bigFile(t, files)
	rsp, err := server.Client().Get(server.URL + "/download?path=big")
	if err != nil {
		t.Fatal(err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK || rsp.ProtoMajor != 2 {
		t.Fatalf("download: %s over HTTP/%d", rsp.Status, rsp.ProtoMajor)
	}
	diskReleased(t, 5*time.Second)
}
