package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"NanoKVM-Server/remotemedia"
	"github.com/gin-gonic/gin"
)

type fakeRemoteDevice struct {
	stopped chan struct{}
	once    sync.Once
}

func (d *fakeRemoteDevice) Close()                   { d.once.Do(func() { close(d.stopped) }) }
func (d *fakeRemoteDevice) Stopped() <-chan struct{} { return d.stopped }

func remoteGadgetFixture(t *testing.T, sizes ...uint64) (*Service, <-chan remotemedia.ReadBlock) {
	t.Helper()
	root := t.TempDir()
	original := []string{usbGadgetUDC, usbStorageLink, mountDevice, forcedEjectDevice, roFlag, cdromFlag, inquiryString}
	open := openRemoteBlockDevice
	usbGadgetUDC = filepath.Join(root, "UDC")
	usbStorageLink = filepath.Join(root, "linked")
	lun := filepath.Join(root, "lun.0")
	if err := os.MkdirAll(lun, 0700); err != nil {
		t.Fatal(err)
	}
	mountDevice, forcedEjectDevice = filepath.Join(lun, "file"), filepath.Join(lun, "forced_eject")
	roFlag, cdromFlag, inquiryString = filepath.Join(lun, "ro"), filepath.Join(lun, "cdrom"), filepath.Join(lun, "inquiry_string")
	for _, name := range []string{mountDevice, forcedEjectDevice, roFlag, cdromFlag, inquiryString, filepath.Join(lun, "dvd")} {
		if err := os.WriteFile(name, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(usbGadgetUDC, []byte("dummy_udc"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(lun, usbStorageLink); err != nil {
		t.Fatal(err)
	}
	reads := make(chan remotemedia.ReadBlock, 4)
	expectedSize := uint64(1048576)
	if len(sizes) > 0 {
		expectedSize = sizes[0]
	}
	openRemoteBlockDevice = func(size uint64, read remotemedia.ReadBlock) (remoteBlockDevice, error) {
		if size != expectedSize {
			t.Errorf("wrong NBD size: %d", size)
		}
		reads <- read
		return &fakeRemoteDevice{stopped: make(chan struct{})}, nil
	}
	s := NewService()
	t.Cleanup(func() {
		s.mu.Lock()
		r := s.remote
		s.mu.Unlock()
		if r != nil {
			r.close()
			waitRemoteState(t, s, false)
		}
		usbGadgetUDC, usbStorageLink, mountDevice, forcedEjectDevice, roFlag, cdromFlag, inquiryString = original[0], original[1], original[2], original[3], original[4], original[5], original[6]
		openRemoteBlockDevice = open
	})
	return s, reads
}

func remoteSMBCall(s *Service, body string, ctx context.Context) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/storage/remote/smb", strings.NewReader(body)).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	s.ConnectSMB(c)
	return w
}

func TestSMBMediaRejectsBusyAndInvalidRequests(t *testing.T) {
	s, _ := remoteGadgetFixture(t)
	for _, body := range []string{`{`, `{"url":"smb://host/share/test.iso"}`, `{"url":"smb://u:p@host/share/test.iso","username":"u"}`, strings.Repeat(" ", 8193)} {
		if rsp := remoteSMBCall(s, body, context.Background()); !strings.Contains(rsp.Body.String(), `"code":-1`) {
			t.Fatal("invalid SMB options accepted")
		}
	}
	s.netboot = &netbootSession{}
	if rsp := remoteSMBCall(s, `{"url":"smb://host/share/test.iso","username":"reader"}`, context.Background()); rsp.Code != 409 {
		t.Fatal("SMB replaced network boot")
	}
	s.netboot = nil
}

func TestSMBMediaIntegration(t *testing.T) {
	address := os.Getenv("NANOKVM_SMB_TEST_ADDR")
	if address == "" {
		t.Skip("real Samba fixture not configured")
	}
	s, reads := remoteGadgetFixture(t, 64*1048576)
	body, _ := json.Marshal(remotemedia.SMBOptions{URL: "smb://" + address + "/images/test.iso", Username: "media", Password: "nano-fixture", Domain: "WORKGROUP"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if rsp := remoteSMBCall(s, string(body), ctx); rsp.Code != 200 {
		t.Fatal(rsp.Body.String())
	}
	cancel()
	waitRemoteState(t, s, true)
	block, err := (<-reads)(64*1048576-4096, 4096)
	if err != nil || len(block) != 4096 {
		t.Fatalf("SMB NBD read: %v", err)
	}
	for i, b := range block {
		p := 1048576 - 4096 + i
		if b != byte((p*37+p/1009)%251) {
			t.Fatal("SMB NBD data mismatch")
		}
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	s.RemoteStatus(c)
	if !strings.Contains(w.Body.String(), `"source":"smb"`) || strings.Contains(w.Body.String(), address) || strings.Contains(w.Body.String(), "nano-fixture") || strings.Contains(w.Body.String(), "WORKGROUP") {
		t.Fatal("incorrect SMB status or leaked credentials")
	}
	if rsp := remoteHTTPCall(s, `{"url":"http://host/image.iso"}`, context.Background()); rsp.Code != 409 {
		t.Fatal("HTTP replaced SMB")
	}
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	s.DisconnectRemote(c)
	waitRemoteState(t, s, false)
	backing, _ := os.ReadFile(mountDevice)
	if strings.TrimSpace(string(backing)) != "" {
		t.Fatal("SMB backing retained after disconnect")
	}
}

func waitRemoteState(t *testing.T, s *Service, mounted bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		r := s.remote
		ready := r == nil
		if mounted {
			ready = r != nil && r.mounted
		}
		s.mu.Unlock()
		if ready {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("remote session did not reach requested state")
}

func remoteHTTPCall(s *Service, body string, ctx context.Context) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/storage/remote/http", strings.NewReader(body)).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	s.ConnectHTTP(c)
	return w
}

func TestHTTPMediaOutlivesRequestAndDisconnects(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, reads := remoteGadgetFixture(t)
	data := bytes.Repeat([]byte{0xa3, 0x72, 0x1f, 0x80}, 262144)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "user" || p != "private" {
			t.Error("missing source credentials")
		}
		w.Header().Set("ETag", `"v1"`)
		http.ServeContent(w, r, "image.iso", time.Unix(1700000000, 0), bytes.NewReader(data))
	}))
	defer server.Close()
	body, _ := json.Marshal(remotemedia.HTTPOptions{URL: server.URL + "/image.iso?token=secret", Username: "user", Password: "private"})
	ctx, cancel := context.WithCancel(context.Background())
	rsp := remoteHTTPCall(s, string(body), ctx)
	if rsp.Code != 200 {
		t.Fatal(rsp.Body.String())
	}
	cancel() // closing the control request must not stop device-owned media
	waitRemoteState(t, s, true)
	read := <-reads
	got, err := read(32768, 4096)
	if err != nil || !bytes.Equal(got, data[32768:36864]) {
		t.Fatalf("block: %v", err)
	}
	for _, path := range []string{roFlag, cdromFlag} {
		b, _ := os.ReadFile(path)
		if string(b) != "1" {
			t.Fatal("not read only optical")
		}
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	s.RemoteStatus(c)
	if strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), server.URL) {
		t.Fatal("source secrets in status")
	}
	if !strings.Contains(w.Body.String(), `"source":"http"`) {
		t.Fatal("missing source discriminator")
	}
	if rsp := remoteHTTPCall(s, string(body), context.Background()); rsp.Code != 409 {
		t.Fatal("concurrent mount accepted")
	}
	if code, _ := s.enableNetboot(context.Background(), false); code != -5 {
		t.Fatal("netboot accepted while ISO mounted")
	}
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	s.DisconnectRemote(c)
	waitRemoteState(t, s, false)
	b, _ := os.ReadFile(mountDevice)
	if strings.TrimSpace(string(b)) != "" {
		t.Fatal("stale NBD backing after disconnect")
	}
}

func TestHTTPMediaRejectsBusyAndInvalidRequests(t *testing.T) {
	s, _ := remoteGadgetFixture(t)
	for _, body := range []string{`{`, `{"url":"file:///tmp/image.iso"}`, `{"url":"http://u:p@host/image.iso"}`, strings.Repeat(" ", 8193)} {
		rsp := remoteHTTPCall(s, body, context.Background())
		if !strings.Contains(rsp.Body.String(), `"code":-1`) {
			t.Fatal("invalid options accepted")
		}
	}
	s.netboot = &netbootSession{}
	rsp := remoteHTTPCall(s, `{"url":"http://host/image.iso"}`, context.Background())
	if rsp.Code != 409 {
		t.Fatal("network boot conflict ignored")
	}
	s.netboot = nil
	os.WriteFile(mountDevice, []byte("/data/local.iso"), 0600)
	if rsp = remoteHTTPCall(s, `{"url":"http://host/image.iso"}`, context.Background()); rsp.Code != 409 {
		t.Fatal("local image replaced")
	}
}
