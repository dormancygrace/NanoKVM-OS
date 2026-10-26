package remotemedia

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const smbTimeout = 10 * time.Second

type SMBOptions struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
	Domain   string `json:"domain"`
}

func parseSMBOptions(o SMBOptions) (address, share, name string, err error) {
	invalid := errors.New("Use smb://server/share/path/image.iso without embedded credentials, a query or a fragment")
	u, e := url.Parse(o.URL)
	if e != nil || u.Scheme != "smb" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || len(o.URL) > 4096 || !utf8.ValidString(o.URL) || !utf8.ValidString(u.Path) {
		return "", "", "", invalid
	}
	port := u.Port()
	if port == "" {
		port = "445"
	}
	n, e := strconv.Atoi(port)
	if e != nil || n < 1 || n > 65535 || strings.ContainsAny(u.Hostname(), "%\\") {
		return "", "", "", invalid
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) < 2 {
		return "", "", "", invalid
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\:") || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || strings.IndexFunc(part, unicode.IsControl) >= 0 {
			return "", "", "", invalid
		}
	}
	if !strings.EqualFold(path.Ext(parts[len(parts)-1]), ".iso") || len(parts[len(parts)-1]) > 200 {
		return "", "", "", errors.New("Select an ISO file on the SMB share")
	}
	if o.Username == "" || len(o.Username) >= 256 || len(o.Domain) >= 256 || len(o.Password) > 1024 || !utf8.ValidString(o.Username) || !utf8.ValidString(o.Domain) || !utf8.ValidString(o.Password) || strings.ContainsAny(o.Username+o.Domain, "\\/:") || strings.IndexFunc(o.Username+o.Domain, unicode.IsControl) >= 0 || strings.ContainsRune(o.Password, 0) {
		return "", "", "", errors.New("Enter an SMB username and optional domain in their separate fields")
	}
	return net.JoinHostPort(u.Hostname(), port), parts[0], strings.Join(parts[1:], "/"), nil
}

func ValidateSMBOptions(o SMBOptions) error {
	_, _, _, err := parseSMBOptions(o)
	return err
}

type smbFile interface {
	ReadAt([]byte, int64) (int, error)
	Stat() (os.FileInfo, error)
}

// SMBSource owns a cancellable kernel CIFS reader and a bounded payload window.
// The reader lives in a child process so a stalled filesystem syscall cannot
// block NBD/USB teardown or write into a buffer after cancellation.
type SMBSource struct {
	Name   string
	Size   uint64
	ctx    context.Context
	cancel context.CancelFunc
	stop   func() bool
	read   func([]byte, uint64) error
	close  func()
	once   sync.Once
	mu     sync.Mutex
	buffer []byte
	start  uint64
	valid  int
	next   uint64
	streak uint8
}

func smbError(ctx context.Context, message string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// System errors can contain the supplied remote path or server details.
	return errors.New(message)
}

func (s *SMBSource) ReadBlock(offset uint64, length uint32) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	if length == 0 || length > MaxRead || offset > s.Size || uint64(length) > s.Size-offset {
		return nil, errors.New("Invalid image read bounds")
	}
	if offset == s.next && length >= 64<<10 {
		s.streak = min(s.streak+1, 2)
	} else {
		s.streak = 0
	}
	s.next = offset + uint64(length)
	if offset >= s.start && offset-s.start+uint64(length) <= uint64(s.valid) {
		return s.buffer[offset-s.start : offset-s.start+uint64(length)], nil
	}
	s.valid = 0
	count := uint64(length)
	if s.streak >= 2 {
		count = min(uint64(MaxRead), s.Size-offset)
	}
	if err := s.read(s.buffer[:count], offset); err != nil {
		return nil, err
	}
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	s.start, s.valid = offset, int(count)
	return s.buffer[:length], nil
}

func (s *SMBSource) Close() {
	s.once.Do(func() {
		s.cancel()
		s.stop()
		s.close()
	})
}

// Used inside the isolated reader. cache=none,actimeo=0 keep kernel caching
// from hiding remote modifications when checking metadata on a cache miss.
type smbReadFile struct {
	file  smbFile
	size  uint64
	mtime time.Time
}

var errSMBChanged = errors.New("Remote image changed; disconnect and reconnect it")

func (r *smbReadFile) read(p []byte, offset uint64) error {
	info, err := r.file.Stat()
	if err != nil {
		return errors.New("Could not read SMB ISO metadata")
	}
	if info.Size() < 0 || uint64(info.Size()) != r.size || !info.ModTime().Equal(r.mtime) {
		return errSMBChanged
	}
	n, err := r.file.ReadAt(p, int64(offset))
	if err != nil || n != len(p) {
		return errors.New("SMB ISO read failed or returned an incomplete block")
	}
	return nil
}
