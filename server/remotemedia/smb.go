package remotemedia

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
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

	smb2 "github.com/cloudsoda/go-smb2"
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
	if e != nil || u.Scheme != "smb" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || len(o.URL) > 4096 || !utf8.ValidString(o.URL) {
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
	if o.Username == "" || len(o.Username) > 256 || len(o.Domain) > 256 || len(o.Password) > 1024 || strings.ContainsAny(o.Username+o.Domain, "\\/:") || strings.IndexFunc(o.Username+o.Domain, unicode.IsControl) >= 0 || strings.ContainsRune(o.Password, 0) {
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

// SMBSource uses a userspace SMB2/3 client. It never mounts a filesystem or
// loads CIFS/ksmbd. Connections and the payload window exist only while mounted.
type SMBSource struct {
	Name   string
	Size   uint64
	ctx    context.Context
	cancel context.CancelFunc
	conn   net.Conn
	stop   func() bool
	file   smbFile
	mtime  time.Time
	mu     sync.Mutex
	buffer []byte
	start  uint64
	valid  int
	next   uint64
	streak uint8
}

func OpenSMB(ctx context.Context, o SMBOptions) (*SMBSource, error) {
	address, share, name, err := parseSMBOptions(o)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	opening, finish := context.WithTimeout(ctx, smbTimeout)
	defer finish()
	conn, err := (&net.Dialer{}).DialContext(opening, "tcp", address)
	if err != nil {
		err = smbError(opening, "Could not connect to the SMB server")
		cancel()
		return nil, err
	}
	s := &SMBSource{ctx: ctx, cancel: cancel, conn: conn, Name: path.Base(name), next: ^uint64(0)}
	s.stop = context.AfterFunc(ctx, func() { _ = conn.Close() })
	fail := func(message string) (*SMBSource, error) {
		err := smbError(opening, message)
		s.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(smbTimeout))
	d := smb2.Dialer{Negotiator: smb2.Negotiator{RequireMessageSigning: true}, Initiator: &smb2.NTLMInitiator{User: o.Username, Password: o.Password, Domain: o.Domain}}
	session, err := d.DialConn(opening, &boundedSMBConn{Conn: conn, headerPos: 4}, address)
	if err != nil {
		return fail("SMB authentication or signed SMB2/3 negotiation failed")
	}
	tree, err := session.WithContext(opening).Mount(share)
	if err != nil {
		return fail("Could not open the SMB share")
	}
	// The file inherits the share context; use the mount lifetime, not the
	// opening timeout. The socket deadline also bounds each read below.
	s.file, err = tree.WithContext(ctx).Open(name)
	if err != nil {
		return fail("Could not open the SMB ISO for reading")
	}
	info, err := s.file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 0 {
		return fail("Could not read SMB ISO metadata")
	}
	if err = ValidateImage(uint64(info.Size())); err != nil {
		s.Close()
		return nil, err
	}
	s.Size, s.mtime = uint64(info.Size()), info.ModTime()
	s.buffer = make([]byte, MaxRead)
	_ = conn.SetDeadline(time.Time{}) // idle media must not expire between reads
	return s, nil
}

func smbError(ctx context.Context, message string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// Library errors can contain the supplied remote path or server details.
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
	_ = s.conn.SetDeadline(time.Now().Add(smbTimeout))
	defer s.conn.SetDeadline(time.Time{})
	info, err := s.file.Stat()
	if err != nil {
		return nil, smbError(s.ctx, "Could not read SMB ISO metadata")
	}
	if info.Size() < 0 || uint64(info.Size()) != s.Size || !info.ModTime().Equal(s.mtime) {
		return nil, errors.New("Remote image changed; disconnect and reconnect it")
	}
	count := uint64(length)
	if s.streak >= 2 {
		count = min(uint64(MaxRead), s.Size-offset)
	}
	n, err := s.file.ReadAt(s.buffer[:count], int64(offset))
	if err != nil || uint64(n) != count {
		return nil, smbError(s.ctx, "SMB ISO read failed or returned an incomplete block")
	}
	s.start, s.valid = offset, n
	return s.buffer[:length], nil
}

func (s *SMBSource) Close() {
	s.cancel()
	s.stop()
	// Closing the owned TCP connection interrupts even a library request which
	// waits for a final SMB response on cancellation. It releases remote handles
	// without racing File.Close against a pending ReadAt.
	_ = s.conn.Close()
}

// Bound a peer's frame length before the client library allocates its receive
// buffer. One 1 MiB read plus SMB3 encryption headers fits well within 2 MiB.
type boundedSMBConn struct {
	net.Conn
	header    [4]byte
	headerPos int
	remaining int
}

func (c *boundedSMBConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if c.headerPos == 4 && c.remaining == 0 {
		if _, err := io.ReadFull(c.Conn, c.header[:]); err != nil {
			return 0, err
		}
		n := binary.BigEndian.Uint32(c.header[:])
		if n < 64 || n > 2*MaxRead {
			return 0, errors.New("Invalid SMB response frame length")
		}
		c.remaining, c.headerPos = int(n), 0
	}
	if c.headerPos < 4 {
		n := copy(p, c.header[c.headerPos:])
		c.headerPos += n
		return n, nil
	}
	n, err := c.Conn.Read(p[:min(len(p), c.remaining)])
	c.remaining -= n
	return n, err
}
