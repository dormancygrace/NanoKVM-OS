package remotemedia

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestSMBHelperProtocol(t *testing.T) {
	name := filepath.Join(t.TempDir(), "test.iso")
	data := httpImage()
	if err := os.WriteFile(name, data, 0600); err != nil {
		t.Fatal(err)
	}
	client, child := net.Pipe()
	defer client.Close()
	done := make(chan error, 1)
	go func() {
		defer child.Close()
		done <- serveSMBHelper(child, func(o smbMountOptions) (*os.File, error) {
			if o.Password != "p,a=ss\nword" || o.Username != "reader" {
				return nil, errors.New("credential corruption")
			}
			return os.Open(name)
		})
	}()
	client.SetDeadline(time.Now().Add(3 * time.Second))
	request, _ := json.Marshal(SMBOptions{URL: "smb://host/share/test.iso", Username: "reader", Password: "p,a=ss\nword"})
	var h [12]byte
	binary.BigEndian.PutUint32(h[:4], uint32(len(request)))
	if _, err := client.Write(h[:4]); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write(request); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(client, h[:]); err != nil {
		t.Fatal(err)
	}
	if binary.BigEndian.Uint32(h[:4]) != smbOK || binary.BigEndian.Uint64(h[4:]) != uint64(len(data)) {
		t.Fatal("incorrect metadata")
	}
	read := func(off uint64, length uint32) uint32 {
		t.Helper()
		binary.BigEndian.PutUint64(h[:8], off)
		binary.BigEndian.PutUint32(h[8:], length)
		if _, err := client.Write(h[:]); err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadFull(client, h[:4]); err != nil {
			t.Fatal(err)
		}
		return binary.BigEndian.Uint32(h[:4])
	}
	for _, off := range []uint64{0, 173, uint64(len(data)) - 4096} {
		if status := read(off, 4096); status != smbOK {
			t.Fatal(status)
		}
		block := make([]byte, 4096)
		if _, err := io.ReadFull(client, block); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(block, data[off:off+4096]) {
			t.Fatal("incorrect payload")
		}
	}
	if err := os.Truncate(name, int64(len(data)-2048)); err != nil {
		t.Fatal(err)
	}
	if status := read(0, 4096); status != smbChanged {
		t.Fatal("changed file accepted")
	}
	if err := <-done; err == nil {
		t.Fatal("failed reader stayed alive")
	}
}

func TestSMBMountIsPrivateAndReadOnly(t *testing.T) {
	address := os.Getenv("NANOKVM_SMB_TEST_ADDR")
	if address == "" {
		t.Skip("requires the kernel CIFS integration fixture")
	}
	relay, err := newSMBRelay(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	before, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		t.Fatal(err)
	}
	f, err := mountSMBFile(smbMountOptions{SMBOptions: SMBOptions{URL: "smb://" + address + "/images/test.iso", Username: "media", Password: "nano-fixture"}, Address: relay.listener.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var st unix.Statfs_t
	if err := unix.Fstatfs(int(f.Fd()), &st); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []int64{unix.ST_RDONLY, unix.ST_NOSUID, unix.ST_NODEV, unix.ST_NOEXEC} {
		if int64(st.Flags)&flag == 0 {
			t.Fatal("missing mount restriction", flag)
		}
	}
	after, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("SMB mount became visible in the mount namespace")
	}
}

// A real CIFS read can block inside the kernel. Gate replies after opening the
// image to test both explicit cancellation and the production read deadline.
func TestSMBStalledRead(t *testing.T) {
	address := os.Getenv("NANOKVM_SMB_TEST_ADDR")
	if address == "" {
		t.Skip("requires the kernel CIFS integration fixture")
	}
	for _, timeout := range []bool{false, true} {
		label := "cancel"
		if timeout {
			label = "deadline"
		}
		t.Run(label, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			var paused atomic.Bool
			gated := make(chan struct{}, 1)
			stop := make(chan struct{})
			defer close(stop)
			go func() {
				for {
					client, err := listener.Accept()
					if err != nil {
						return
					}
					go func() {
						upstream, err := net.DialTimeout("tcp", address, time.Second)
						if err != nil {
							client.Close()
							return
						}
						closed := make(chan struct{})
						go func() {
							select {
							case <-stop:
								client.Close()
								upstream.Close()
							case <-closed:
							}
						}()
						defer close(closed)
						defer client.Close()
						defer upstream.Close()
						go func() { io.Copy(upstream, client); upstream.Close() }()
						buffer := make([]byte, 64<<10)
						for {
							n, err := upstream.Read(buffer)
							if n > 0 {
								if paused.Load() {
									select {
									case gated <- struct{}{}:
									default:
									}
									<-stop
									return
								}
								if _, e := client.Write(buffer[:n]); e != nil {
									return
								}
							}
							if err != nil {
								return
							}
						}
					}()
				}
			}()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s, err := OpenSMB(ctx, SMBOptions{URL: "smb://" + listener.Addr().String() + "/images/test.iso", Username: "media", Password: "nano-fixture"})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			paused.Store(true)
			done := make(chan error, 1)
			started := time.Now()
			go func() { _, err := s.ReadBlock(0, 4096); done <- err }()
			select {
			case <-gated:
			case <-time.After(3 * time.Second):
				t.Fatal("read did not reach stalled peer")
			}
			if !timeout {
				cancel()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("stalled read succeeded")
				}
				if !timeout && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if timeout && time.Since(started) < smbTimeout-time.Second {
					t.Fatal("did not exercise read deadline")
				}
			case <-time.After(smbTimeout + 2*time.Second):
				t.Fatal("kernel read blocked teardown")
			}
			s.Close()
			// SIGKILL must reap the helper even while this peer still withholds
			// replies. Otherwise repeated opens could strand process resources.
			reapDeadline := time.Now().Add(2 * time.Second)
			for len(smbReaderSlot) != 0 && time.Now().Before(reapDeadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if len(smbReaderSlot) != 0 {
				t.Fatal("cancelled reader was not reaped")
			}
		})
	}
}

func TestSMBHelperRejectsOversizedOptionsBeforeMount(t *testing.T) {
	for _, length := range []uint32{0, 8193, ^uint32(0)} {
		var input bytes.Buffer
		binary.Write(&input, binary.BigEndian, length)
		err := serveSMBHelper(&input, func(smbMountOptions) (*os.File, error) { t.Fatal("invalid message reached mount"); return nil, nil })
		if err == nil {
			t.Fatal("invalid options size accepted")
		}
	}
}

func TestSMBHelperRejectsReadBounds(t *testing.T) {
	for _, tc := range []struct {
		off    uint64
		length uint32
	}{{0, 0}, {0, MaxRead + 1}, {^uint64(0), 512}, {4096, 512}} {
		name := filepath.Join(t.TempDir(), "test.iso")
		if err := os.WriteFile(name, make([]byte, 4096), 0600); err != nil {
			t.Fatal(err)
		}
		client, child := net.Pipe()
		done := make(chan error, 1)
		go func() {
			defer child.Close()
			done <- serveSMBHelper(child, func(smbMountOptions) (*os.File, error) { return os.Open(name) })
		}()
		client.SetDeadline(time.Now().Add(time.Second))
		binary.Write(client, binary.BigEndian, uint32(2))
		client.Write([]byte("{}"))
		var h [12]byte
		if _, err := io.ReadFull(client, h[:]); err != nil {
			t.Fatal(err)
		}
		binary.BigEndian.PutUint64(h[:8], tc.off)
		binary.BigEndian.PutUint32(h[8:], tc.length)
		client.Write(h[:])
		if _, err := client.Read(h[:]); err == nil {
			t.Fatal("invalid read accepted")
		}
		client.Close()
		if err := <-done; err == nil {
			t.Fatal("invalid read not rejected")
		}
	}
}

// Closing a source must release an in-flight read without waiting for its
// mutex, and no payload may be returned after cancellation.
func TestSMBCloseInterruptsRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	released := make(chan struct{})
	s := &SMBSource{ctx: ctx, cancel: cancel, Size: MaxRead, buffer: make([]byte, MaxRead), next: ^uint64(0)}
	s.close = func() { close(released) }
	s.stop = context.AfterFunc(ctx, func() {})
	s.read = func([]byte, uint64) error { close(entered); <-released; return ctx.Err() }
	done := make(chan error, 1)
	go func() { _, err := s.ReadBlock(0, 4096); done <- err }()
	<-entered
	s.Close()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close blocked on read mutex")
	}
	s.Close()
}
