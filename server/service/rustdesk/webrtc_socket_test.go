//go:build linux

package rustdesk

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRustDeskUnixSocketPeerCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "webrtc.sock")
	listener, err := listenSocket(path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("socket permissions", err)
	}
	raw := listener.(privateListener).Listener
	credential := make(chan bool, 1)
	go func() {
		conn, err := raw.Accept()
		if err != nil {
			credential <- false
			return
		}
		defer conn.Close()
		credential <- rootPeer(conn)
	}()
	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	client.Close()
	if got := <-credential; got != (os.Geteuid() == 0) {
		t.Fatal("SO_PEERCRED did not match root requirement", got, os.Geteuid())
	}
	accepted := make(chan net.Conn, 1)
	go func() { conn, _ := listener.Accept(); accepted <- conn }()
	client, err = net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if os.Geteuid() == 0 {
		select {
		case conn := <-accepted:
			if conn == nil {
				t.Fatal("root rejected")
			}
			conn.Close()
		case <-time.After(time.Second):
			t.Fatal("root accept stalled")
		}
	} else {
		client.SetReadDeadline(time.Now().Add(time.Second))
		var b [1]byte
		if _, err = client.Read(b[:]); !errors.Is(err, io.EOF) {
			t.Fatal("non-root peer not closed", err)
		}
		listener.Close()
		select {
		case conn := <-accepted:
			if conn != nil {
				conn.Close()
				t.Fatal("non-root peer accepted")
			}
		case <-time.After(time.Second):
			t.Fatal("accept did not cancel")
		}
	}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if rootPeer(a) {
		t.Fatal("non-Unix connection trusted")
	}
}
