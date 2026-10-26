package remotemedia

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSMBOptions(t *testing.T) {
	for _, raw := range []string{"smb://host/share/a.iso", "smb://[::1]:1445/share/directory/a%20b.ISO"} {
		if err := ValidateSMBOptions(SMBOptions{URL: raw, Username: "reader", Domain: "WORKGROUP"}); err != nil {
			t.Fatal(raw, err)
		}
	}
	for _, raw := range []string{"http://host/a.iso", "smb://host/share", "smb://host/share/a.img", "smb://user:secret@host/share/a.iso", "smb://host/share/a.iso?secret", "smb://host/share/a.iso#fragment", "smb://host:0/share/a.iso", "smb://host:65536/share/a.iso", "smb:///share/a.iso", "smb://host/share/../a.iso", "smb://host/share/%2e%2e/a.iso", "smb://host/share/a%5cb.iso", "smb://host/share/a.iso:stream", "smb://host/share//a.iso", "smb://host/share/a%00.iso", "smb://host/share/a.iso."} {
		if err := ValidateSMBOptions(SMBOptions{URL: raw, Username: "reader"}); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	for _, user := range []string{"", "domain\\user", "bad\nuser", strings.Repeat("u", 257)} {
		if ValidateSMBOptions(SMBOptions{URL: "smb://host/share/a.iso", Username: user}) == nil {
			t.Errorf("accepted username %q", user)
		}
	}
}

func TestSMBReadBoundsCacheAndChange(t *testing.T) {
	data := httpImage()
	name := filepath.Join(t.TempDir(), "test.iso")
	if err := os.WriteFile(name, data, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, _ := f.Stat()
	ctx, cancel := context.WithCancel(context.Background())
	reader := &smbReadFile{file: f, size: uint64(len(data)), mtime: info.ModTime()}
	s := &SMBSource{ctx: ctx, cancel: cancel, read: reader.read, close: func() {}, Size: reader.size, buffer: make([]byte, MaxRead), next: ^uint64(0)}
	s.stop = context.AfterFunc(ctx, func() {})
	defer s.Close()
	for _, offset := range []uint64{0, 65536, 131072, 196608, 131000, uint64(len(data)) - 65536, 0} {
		got, err := s.ReadBlock(offset, 65536)
		if err != nil || !bytes.Equal(got, data[offset:offset+65536]) {
			t.Fatalf("read at %d: %v", offset, err)
		}
	}
	for _, tc := range []struct {
		offset uint64
		length uint32
	}{{0, 0}, {0, MaxRead + 1}, {uint64(len(data)), 512}, {^uint64(0), 512}} {
		if _, err := s.ReadBlock(tc.offset, tc.length); err == nil {
			t.Fatal("invalid read bounds accepted")
		}
	}
	if err := os.Truncate(name, int64(len(data)-2048)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadBlock(2097152, 512); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("changed source accepted: %v", err)
	}
	s.Close()
	if _, err := s.ReadBlock(0, 512); !errors.Is(err, context.Canceled) {
		t.Fatalf("data served after close: %v", err)
	}
}

func TestSMBCancelNegotiation(t *testing.T) {
	if os.Getenv("NANOKVM_SMB_TEST_ADDR") == "" {
		t.Skip("requires the kernel CIFS integration fixture")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() { c, _ := listener.Accept(); accepted <- c }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		s, err := OpenSMB(ctx, SMBOptions{URL: "smb://" + listener.Addr().String() + "/share/test.iso", Username: "user", Password: "private"})
		if s != nil {
			s.Close()
		}
		done <- err
	}()
	peer := <-accepted
	defer peer.Close()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt SMB handshake")
	}
}

// Run against a real signed Samba fixture, including an encryption-required
// share, with NANOKVM_SMB_TEST_ADDR=127.0.0.1:3445. Requires root/CAP_SYS_ADMIN and a kernel with CIFS and the new mount API.
func TestSMBIntegration(t *testing.T) {
	address := os.Getenv("NANOKVM_SMB_TEST_ADDR")
	if address == "" {
		t.Skip("set NANOKVM_SMB_TEST_ADDR for the Samba integration fixture")
	}
	for _, share := range []string{"images", "encrypted"} {
		t.Run(share, func(t *testing.T) {
			o := SMBOptions{URL: "smb://" + address + "/" + share + "/test.iso", Username: "media", Password: "nano-fixture", Domain: ""}
			s, err := OpenSMB(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if s.Size != 64*MaxRead || s.Name != "test.iso" {
				t.Fatalf("metadata %s/%d", s.Name, s.Size)
			}
			for _, off := range []uint64{0, 65536, 131072, 196608, 123456, s.Size - 65536, 0} {
				block, err := s.ReadBlock(off, 65536)
				if err != nil {
					t.Fatal(err)
				}
				for i, b := range block {
					p := (int(off) + i) % MaxRead
					if b != byte((p*37+p/1009)%251) {
						t.Fatalf("incorrect byte at %d", int(off)+i)
					}
				}
			}
			s.Close()
			o.Password = "incorrect"
			if bad, err := OpenSMB(context.Background(), o); err == nil {
				bad.Close()
				t.Fatal("incorrect password accepted")
			} else if strings.Contains(err.Error(), "incorrect") || strings.Contains(err.Error(), address) {
				t.Fatal("source details leaked in failure")
			}
		})
	}
}
