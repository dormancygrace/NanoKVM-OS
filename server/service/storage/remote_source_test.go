package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/gorilla/websocket"
	"golang.org/x/sys/unix"
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

func TestHTTPMediaCancelDuringProbe(t *testing.T) {
	s, reads := remoteGadgetFixture(t)
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer server.Close()
	body, _ := json.Marshal(remotemedia.HTTPOptions{URL: server.URL})
	remoteHTTPCall(s, string(body), context.Background())
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("probe did not start")
	}
	s.mu.Lock()
	r := s.remote
	s.mu.Unlock()
	r.close()
	waitRemoteState(t, s, false)
	select {
	case <-reads:
		t.Fatal("cancelled probe opened NBD")
	default:
	}
	s.mu.Lock()
	err := s.remoteError
	s.mu.Unlock()
	if err != "" {
		t.Fatalf("cancel recorded as failure: %s", err)
	}
}

func TestHTTPMediaReadFailureReleasesGadget(t *testing.T) {
	s, reads := remoteGadgetFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "bytes=0-2047" {
			w.WriteHeader(503)
			return
		}
		http.ServeContent(w, r, "image.iso", time.Time{}, bytes.NewReader(make([]byte, 1048576)))
	}))
	defer server.Close()
	body, _ := json.Marshal(remotemedia.HTTPOptions{URL: server.URL})
	remoteHTTPCall(s, string(body), context.Background())
	waitRemoteState(t, s, true)
	read := <-reads
	if _, err := read(4096, 512); err == nil {
		t.Fatal("failed source returned data")
	}
	waitRemoteState(t, s, false)
	s.mu.Lock()
	err := s.remoteError
	s.mu.Unlock()
	if !strings.Contains(err, "503") {
		t.Fatalf("missing failure: %q", err)
	}
	b, _ := os.ReadFile(mountDevice)
	if strings.TrimSpace(string(b)) != "" {
		t.Fatal("failed source retained by gadget")
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

func TestBrowserReceivesAttachmentFailure(t *testing.T) {
	s, _ := remoteGadgetFixture(t)
	openRemoteBlockDevice = func(uint64, remotemedia.ReadBlock) (remoteBlockDevice, error) {
		return nil, errors.New("NBD unavailable")
	}
	router := gin.New()
	router.GET("/connect", s.ConnectRemote)
	server := httptest.NewServer(router)
	defer server.Close()
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/connect", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := ws.WriteJSON(map[string]any{"type": "open", "name": "image.iso", "size": 1048576}); err != nil {
		t.Fatal(err)
	}
	var reply map[string]any
	if err := ws.ReadJSON(&reply); err != nil {
		t.Fatalf("failure message lost: %v", err)
	}
	if reply["type"] != "error" || reply["message"] != "NBD unavailable" {
		t.Fatalf("wrong reply: %v", reply)
	}
	waitRemoteState(t, s, false)
}

func TestRemoteCleanupRespectsDeviceOwner(t *testing.T) {
	s, _ := remoteGadgetFixture(t)
	root := t.TempDir()
	devicePath, pidPath := filepath.Join(root, "nbd0"), filepath.Join(root, "pid")
	device, err := os.Create(devicePath)
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()
	if err := os.WriteFile(mountDevice, []byte(remotemedia.DevicePath), 0600); err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(int(device.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	s.cleanupRemote(devicePath, pidPath) // the lock can be held before the NBD PID appears
	assertBacking := func(want string) {
		t.Helper()
		data, err := os.ReadFile(mountDevice)
		if err != nil || strings.TrimSpace(string(data)) != want {
			t.Fatalf("backing = %q, %v; want %q", data, err, want)
		}
	}
	assertBacking(remotemedia.DevicePath)
	if err := unix.Flock(int(device.Fd()), unix.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidPath, []byte("123"), 0600); err != nil {
		t.Fatal(err)
	}
	s.cleanupRemote(devicePath, pidPath) // an external owner need not use our flock
	assertBacking(remotemedia.DevicePath)
	if err := os.Remove(pidPath); err != nil {
		t.Fatal(err)
	}
	s.cleanupRemote(devicePath, pidPath)
	assertBacking("")
}

type shortRemoteSource struct{ closed chan struct{} }

func (s *shortRemoteSource) ReadBlock(_ uint64, length uint32) ([]byte, error) {
	return make([]byte, length-1), nil
}
func (s *shortRemoteSource) Close() { close(s.closed) }

func TestRemoteShortReadDetaches(t *testing.T) {
	s, reads := remoteGadgetFixture(t)
	r := &remoteSession{kind: "http", size: 1048576, closed: make(chan struct{})}
	if err := s.reserveRemote(r); err != nil {
		t.Fatal(err)
	}
	source := &shortRemoteSource{closed: make(chan struct{})}
	go func() { s.releaseRemote(r, s.serveRemote(r, source, nil)) }()
	waitRemoteState(t, s, true)
	if _, err := (<-reads)(0, 512); err == nil {
		t.Fatal("short source read accepted")
	}
	waitRemoteState(t, s, false)
	select {
	case <-source.closed:
	default:
		t.Fatal("failed source not closed")
	}
	data, err := os.ReadFile(mountDevice)
	if err != nil || strings.TrimSpace(string(data)) != "" {
		t.Fatal("short source retained by USB gadget")
	}
}
