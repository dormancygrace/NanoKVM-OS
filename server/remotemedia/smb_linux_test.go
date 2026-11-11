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
		err := serveSMBHelper(&input, nil, func(smbMountOptions) (*os.File, error) { t.Fatal("invalid message reached mount"); return nil, nil })
		if err == nil {
			t.Fatal("invalid options size accepted")
		}
	}
}

// The reader fills the named part of the memory it shares with the server and
// nothing else; a part outside one window buffer ends it without a read.
func TestSMBHelperReadsIntoSharedMemory(t *testing.T) {
	memory, parent, err := newSMBShared()
	if err != nil {
		t.Fatal(err)
	}
	defer memory.Close()
	defer unix.Munmap(parent)
	reader, err := unix.Mmap(int(memory.Fd()), 0, 2*MaxRead, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	image := httpImage()[:8192]
	name := filepath.Join(t.TempDir(), "test.iso")
	if err == nil {
		defer unix.Munmap(reader)
		err = os.WriteFile(name, image, 0600)
	}
	if err != nil {
		t.Fatal(err)
	}
	target := parent[MaxRead:][:4096]
	at, inside := smbSharedOffset(parent, target)
	_, crossing := smbSharedOffset(parent, parent[MaxRead-512:MaxRead+512])
	if _, foreign := smbSharedOffset(parent, make([]byte, 512)); !inside || at != MaxRead || crossing || foreign {
		t.Fatal("window buffer not located", at, inside, crossing, foreign)
	}
	for i, tc := range [][3]uint64{{4096, 4096, uint64(at)}, {0, 0, 0}, {0, MaxRead + 1, 0}, {^uint64(0), 512, 0}, {8192, 512, 0},
		{0, 512, 2 * MaxRead}, {0, 512, 2*MaxRead - 256}, {0, 512, MaxRead - 256}} {
		client, child := net.Pipe()
		done := make(chan error, 1)
		go func() {
			defer child.Close()
			done <- serveSMBHelper(child, reader, func(smbMountOptions) (*os.File, error) { return os.Open(name) })
		}()
		client.SetDeadline(time.Now().Add(time.Second))
		client.Write([]byte{0, 0, 0, 2, '{', '}'})
		var h [16]byte
		if _, err := io.ReadFull(client, h[:12]); err != nil {
			t.Fatal(err)
		}
		binary.BigEndian.PutUint64(h[:8], tc[0])
		binary.BigEndian.PutUint32(h[8:12], uint32(tc[1]))
		binary.BigEndian.PutUint32(h[12:], uint32(tc[2]))
		client.Write(h[:])
		// Only the first request is valid.
		if _, err := io.ReadFull(client, h[:4]); (err == nil && binary.BigEndian.Uint32(h[:4]) == smbOK) != (i == 0) {
			t.Fatal("read request", tc, err)
		}
		client.Close()
		if err := <-done; err == nil {
			t.Fatal("reader did not stop")
		}
	}
	seen := bytes.Equal(target, image[4096:])
	clear(target)
	if !seen || !bytes.Equal(parent, make([]byte, 2*MaxRead)) {
		t.Fatal("block not in the server's buffer, or written outside it", seen)
	}
}
