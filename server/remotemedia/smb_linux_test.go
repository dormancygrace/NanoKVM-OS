package remotemedia

import (
	"bytes"
	"context"
	"encoding/binary"

	"io"
	"net"
	"os"
	"path/filepath"

	"testing"
	"time"

	"golang.org/x/sys/unix"
)

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

// SIGKILL must reap the helper even while this peer still withholds
// replies. Otherwise repeated opens could strand process resources.

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
