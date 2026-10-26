package remotemedia

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// The CI host uses its distribution kernel. This check qualifies NanoKVM's
// CIFS signature-error propagation patch against a real signed Samba peer.
func TestSMBRejectModifiedSignature(t *testing.T) {
	if os.Getenv("NANOKVM_SMB_TEST_STRICT_SIGNATURES") != "1" {
		t.Skip("requires the patched NanoKVM kernel")
	}
	address := os.Getenv("NANOKVM_SMB_TEST_ADDR")
	if address == "" {
		t.Fatal("missing Samba fixture")
	}
	for _, tc := range []struct {
		name    string
		command uint16
	}{
		{"read", 8},
		{"metadata", 16},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proxy, modified := corruptSMBResponse(t, address, tc.command)
			s, err := OpenSMB(context.Background(), SMBOptions{
				URL: "smb://" + proxy + "/images/test.iso", Username: "media", Password: "nano-fixture",
			})
			if tc.command == 8 {
				if err != nil {
					t.Fatal("opening failed before the read fault:", err)
				}
				defer s.Close()
				_, err = s.ReadBlock(0, 65536)
			} else if s != nil {
				defer s.Close()
				// Early QUERY_INFO calls can be optional share probes. Keep
				// corrupting metadata through a required file revalidation.
				time.Sleep(20 * time.Millisecond)
				_, err = s.ReadBlock(0, 65536)
			}
			if !modified.Load() {
				t.Fatal("fixture did not modify a signed response")
			}
			if err == nil {
				t.Fatal("accepted a response with a modified signature")
			}
			t.Log("modified signature rejected:", err)
		})
	}
}

func corruptSMBResponse(t *testing.T, address string, command uint16) (string, *atomic.Bool) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	modified := new(atomic.Bool)
	go func() {
		defer close(done)
		front, err := listener.Accept()
		if err != nil {
			return
		}
		defer front.Close()
		back, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", address)
		if err != nil {
			return
		}
		defer back.Close()
		stop := context.AfterFunc(ctx, func() { front.Close(); back.Close() })
		defer stop()
		front.SetDeadline(time.Now().Add(20 * time.Second))
		back.SetDeadline(time.Now().Add(20 * time.Second))
		forwardDone := make(chan struct{})
		go func() {
			defer close(forwardDone)
			io.Copy(back, front)
			back.Close()
		}()
		defer func() { front.Close(); back.Close(); <-forwardDone }()
		for {
			var header [4]byte
			if _, err := io.ReadFull(back, header[:]); err != nil {
				return
			}
			size := binary.BigEndian.Uint32(header[:])
			if size > 16*1024*1024 {
				return
			}
			packet := make([]byte, size)
			if _, err := io.ReadFull(back, packet); err != nil {
				return
			}
			// Walk compound SMB2 responses; skip unsigned and encrypted frames.
			for offset := 0; (!modified.Load() || command == 16) && offset+64 <= len(packet); {
				p := packet[offset:]
				if !bytes.Equal(p[:4], []byte{0xfe, 'S', 'M', 'B'}) {
					break
				}
				if binary.LittleEndian.Uint16(p[12:14]) == command &&
					binary.LittleEndian.Uint32(p[16:20])&8 != 0 {
					p[48] ^= 1 // Signature only: the image bytes remain untouched.
					modified.Store(true)
					break
				}
				next := binary.LittleEndian.Uint32(p[20:24])
				if next < 64 || next%8 != 0 || uint64(next) > uint64(len(p)) {
					break
				}
				offset += int(next)
			}
			if _, err := io.Copy(front, io.MultiReader(bytes.NewReader(header[:]), bytes.NewReader(packet))); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		listener.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("signature fault proxy did not stop")
		}
	})
	return listener.Addr().String(), modified
}
