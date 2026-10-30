package vm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"math/rand"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func unpackDevArchive(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	files := map[string]string{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return files
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		files[strings.TrimPrefix(h.Name, "nanokvm-dev-archive/")] = string(b)
	}
}

func TestDevArchiveMasksAndHides(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("network/addr", "inet 192.168.4.128/24 brd 192.168.4.255\nlink/ether 48:da:35:6f:1a:2b\n")
	write("network/iw-link", "Connected to aa:bb:cc:dd:ee:ff (on wlan0)\n\tSSID: HomeNetwork\n")
	write("config/etc_kvm_server.yaml", "proto: https\nauthentication:\n  jwt:\n    secretKey: hunter2\n    refreshTokenDuration: 720\nturn:\n    turnCred: abc123\n")
	// Shapes a line rule misses: a flow mapping and a block scalar.
	write("config/etc_kvm_flow.yaml", "proto: http\nturn: {turnUser: alice, turnCred: flow-value-739}\nwebrtc:\n  turnCred: |-\n    block-value-739\n")
	write("config/etc_kvm_mcp.json", `{"enabled": true, "auth": {"apiKey": "json-value-739"}, "port": 8765}`)
	write("config/etc_kvm_broken.yaml", "proto: [unclosed\n  turnCred: broken-value-739\n")
	write("config/etc_kvm_memory.conf", "# memory\nzram_size=128\nswappiness=100\n")
	write("hardware/run-nanokvm/run_nanokvm_screen-text.bgr", "screen text pixels\n")
	write("hardware/run-nanokvm/run_nanokvm_width", "1920\n")
	write("logs/current/application.log", "[hdmi] diagnostics ready fw=0x00000300 model=LT6911UXC chip_id=0x0417\nlogin password=letmein\n")
	write("hardware/edid.bin", "\x00\xff\xff\xff\xff\xff\xff\x00")
	write("system/model", "Sipeed NanoKVM PCIe\x00")
	write("collector", "nanokvm-dev-archive 1\n")
	write("logs/dmesg", "rwnx_send_sm_connect_req drv_vif_index:0 connect to of Loft Mesh(9) channel:5180\nwlan0: joined Attic-5G\n")

	// The saved networks live outside the collection: names only, for masking.
	etc := t.TempDir()
	previous := devArchiveEtc
	devArchiveEtc = etc
	t.Cleanup(func() { devArchiveEtc = previous })
	if err := os.MkdirAll(filepath.Join(etc, "wifi.saved", "4c6f6674204d657368"), 0o700); err != nil { // "Loft Mesh"
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(etc, "wifi.ssid"), []byte("Attic-5G\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	data, err := packDevArchive(dir, map[string]string{"schema": "nanokvm-diagnostics/v1"}, "", time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	files := unpackDevArchive(t, data)
	all := ""
	for _, text := range files {
		all += text
	}
	for _, leaked := range []string{"192.168.4.128", "48:da:35", "aa:bb:cc", "HomeNetwork", "hunter2", "abc123", "letmein", "alice", "flow-value", "block-value", "json-value", "broken-value", "screen text pixels", "Loft Mesh", "Attic-5G"} {
		if strings.Contains(all, leaked) {
			t.Errorf("%q leaked", leaked)
		}
	}
	if !strings.Contains(files["logs/current/application.log"], "chip_id=0x0417") {
		t.Errorf("receiver diagnostics lost: %q", files["logs/current/application.log"])
	}
	for _, omitted := range []string{"hardware/edid.bin", "hardware/run-nanokvm/run_nanokvm_screen-text.bgr", "config/etc_kvm_broken.yaml"} {
		if _, ok := files[omitted]; ok {
			t.Errorf("%s packed", omitted)
		}
	}
	if !strings.Contains(files["config/etc_kvm_flow.yaml"], "proto: http") || !strings.Contains(files["config/etc_kvm_mcp.json"], `"apiKey": "<hidden>"`) {
		t.Errorf("settings lost: %q %q", files["config/etc_kvm_flow.yaml"], files["config/etc_kvm_mcp.json"])
	}
	if !strings.Contains(files["config/etc_kvm_server.yaml"], "refreshTokenDuration: 720") {
		t.Errorf("non-secret setting hidden: %q", files["config/etc_kvm_server.yaml"])
	}
	if !strings.Contains(files["config/etc_kvm_memory.conf"], "zram_size=128\nswappiness=100") {
		t.Errorf("KEY=value settings changed: %q", files["config/etc_kvm_memory.conf"])
	}
	if files["hardware/run-nanokvm/run_nanokvm_width"] != "1920\n" {
		t.Errorf("status file = %q", files["hardware/run-nanokvm/run_nanokvm_width"])
	}
	if files["system/model"] != "Sipeed NanoKVM PCIe\n" {
		t.Errorf("model = %q", files["system/model"])
	}
	if !strings.Contains(files["README.txt"], "hardware/edid.bin") || !strings.Contains(files["README.txt"], "nanokvm-dev-archive 1") {
		t.Errorf("README = %q", files["README.txt"])
	}
	if !strings.Contains(files["diagnostics.json"], "nanokvm-diagnostics/v1") {
		t.Errorf("diagnostics.json = %q", files["diagnostics.json"])
	}
}

func devArchiveReply(t *testing.T) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/vm/diagnostics/archive", nil)
	(&Service{}).DownloadDevArchive(c)
	return w.Body.String()
}

func TestDevArchiveRefusals(t *testing.T) {
	previous := devArchiveScript
	t.Cleanup(func() { devArchiveScript = previous })
	devArchiveScript = filepath.Join(t.TempDir(), "missing")
	if reply := devArchiveReply(t); !strings.Contains(reply, "update nanokvm-base") {
		t.Fatalf("missing collector: %s", reply)
	}
	devArchiveBusy.Lock()
	defer devArchiveBusy.Unlock()
	if reply := devArchiveReply(t); !strings.Contains(reply, "already being prepared") {
		t.Fatalf("busy: %s", reply)
	}
}

func TestReadLimitedKeepsTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 5000)+"tail"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := readLimited(path, 10)
	if err != nil || string(data) != "xxxxxxtail" {
		t.Fatalf("readLimited = %q, %v", data, err)
	}
}

func TestDevArchiveSizeLimit(t *testing.T) {
	dir := t.TempDir()
	previous := devArchiveLimit
	devArchiveLimit = 1024
	t.Cleanup(func() { devArchiveLimit = previous })
	// Pseudo-random words that gzip cannot shrink below the limit. Spaces
	// keep the lines from looking like encoded keys, which would be hidden.
	var b strings.Builder
	random := rand.New(rand.NewSource(1))
	for i := 0; i < 20000; i++ {
		b.WriteByte(byte('a' + random.Intn(26)))
		if i%6 == 5 {
			b.WriteByte(' ')
		}
		if i%80 == 79 {
			b.WriteByte('\n')
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "big"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := packDevArchive(dir, nil, "", time.Unix(0, 0)); err == nil {
		t.Fatal("oversized archive accepted")
	}
}
