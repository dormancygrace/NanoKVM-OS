package remotemedia

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const httpReadTimeout = 10 * time.Second

type HTTPOptions struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// HTTPSource stores at most one MiB of image data, in memory only. It keeps the
// URL (including signed query parameters) and credentials out of public state.
type HTTPSource struct {
	Name               string
	Size               uint64
	ctx                context.Context
	cancel             context.CancelFunc
	client             *http.Client
	transport          *http.Transport
	url                *url.URL
	authOrigin         string
	username, password string
	etag, modified     string
	mu                 sync.Mutex
	buffer             []byte
	start              uint64
	valid              int
	next               uint64
	streak             uint8
}

func ValidateHTTPOptions(o HTTPOptions) error {
	u, err := url.Parse(o.URL)
	if err != nil || !validHTTPURL(u) || len(o.URL) > 4096 {
		return errors.New("Use an HTTP or HTTPS image URL without embedded credentials or a fragment")
	}
	if len(o.Username) > 256 || len(o.Password) > 1024 || strings.ContainsAny(o.Username, ":\r\n") || (o.Username == "" && o.Password != "") {
		return errors.New("Invalid HTTP credentials")
	}
	return nil
}

func validHTTPURL(u *url.URL) bool {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		return err == nil && n > 0 && n <= 65535
	}
	return true
}

func httpOrigin(u *url.URL) string { return strings.ToLower(u.Scheme + "://" + u.Host) }

func OpenHTTP(ctx context.Context, o HTTPOptions) (*HTTPSource, error) {
	if err := ValidateHTTPOptions(o); err != nil {
		return nil, err
	}
	u, _ := url.Parse(o.URL)
	ctx, cancel := context.WithCancel(ctx)
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil // A device-side mount uses the explicitly selected server.
	t.DisableCompression = true
	t.MaxConnsPerHost = 1
	t.MaxIdleConnsPerHost = 1
	t.MaxResponseHeaderBytes = 32 << 10
	t.ResponseHeaderTimeout = httpReadTimeout
	s := &HTTPSource{ctx: ctx, cancel: cancel, transport: t, url: u,
		authOrigin: httpOrigin(u), username: o.Username, password: o.Password,
		buffer: make([]byte, MaxRead), next: ^uint64(0)}
	s.client = &http.Client{Transport: t, Timeout: httpReadTimeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		// A signed query is a credential too; do not copy it into Referer.
		req.Header.Del("Referer")
		if len(via) >= 5 || !validHTTPURL(req.URL) || (via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https") {
			return errors.New("Unsupported image redirect")
		}
		if httpOrigin(req.URL) != s.authOrigin {
			req.Header.Del("Authorization")
		}
		return nil
	}}
	// Probe an actual range. HEAD and Accept-Ranges alone do not establish that
	// the server supports random access. This also works when HEAD is disabled.
	if err := s.fetch(0, s.buffer[:2048], true); err != nil {
		s.Close()
		return nil, err
	}
	s.valid = 2048
	s.Name = path.Base(s.url.Path)
	if !utf8.ValidString(s.Name) || len(s.Name) > 200 || strings.IndexFunc(s.Name, unicode.IsControl) >= 0 || !strings.EqualFold(path.Ext(s.Name), ".iso") {
		s.Name = "remote.iso"
	}
	return s, nil
}

var contentRange = regexp.MustCompile(`^bytes ([0-9]+)-([0-9]+)/([0-9]+)$`)

func parseContentRange(value string) (start, end, total uint64, ok bool) {
	m := contentRange.FindStringSubmatch(value)
	if m == nil {
		return
	}
	var err error
	if start, err = strconv.ParseUint(m[1], 10, 64); err != nil {
		return
	}
	if end, err = strconv.ParseUint(m[2], 10, 64); err != nil {
		return
	}
	if total, err = strconv.ParseUint(m[3], 10, 64); err != nil {
		return
	}
	ok = start <= end && end < total
	return
}

func strongHTTPETag(s string) bool {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return false
	}
	for i := 1; i < len(s)-1; i++ {
		if s[i] < 0x21 || s[i] == '"' || s[i] == 0x7f {
			return false
		}
	}
	return true
}

func (s *HTTPSource) fetch(offset uint64, target []byte, probe bool) error {
	req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, s.url.String(), nil)
	if err != nil {
		return errors.New("Invalid image request")
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, offset+uint64(len(target))-1))
	req.Header.Set("Accept-Encoding", "identity")
	if s.username != "" && httpOrigin(s.url) == s.authOrigin {
		req.SetBasicAuth(s.username, s.password)
	}
	if s.etag != "" {
		req.Header.Set("If-Match", s.etag)
	} else if s.modified != "" {
		req.Header.Set("If-Unmodified-Since", s.modified)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		if s.ctx.Err() != nil {
			return s.ctx.Err()
		}
		// net/url errors contain the complete URL, possibly including secrets.
		return errors.New("Remote HTTP request failed; check the connection and TLS certificate")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("Image server returned HTTP %d; byte-range responses (206) are required", resp.StatusCode)
	}
	start, end, total, ok := parseContentRange(resp.Header.Get("Content-Range"))
	if !ok || start != offset || end-start+1 != uint64(len(target)) || (resp.ContentLength >= 0 && resp.ContentLength != int64(len(target))) {
		return errors.New("Image server returned an incorrect byte range")
	}
	if enc := resp.Header.Get("Content-Encoding"); enc != "" && !strings.EqualFold(enc, "identity") {
		return errors.New("Compressed HTTP image responses are not supported")
	}
	if probe {
		if err := ValidateImage(total); err != nil {
			return err
		}
		s.Size = total
		s.url = resp.Request.URL // Pin the resolved URL; do not repeat a redirect selector.
		if tag := resp.Header.Get("ETag"); strongHTTPETag(tag) {
			s.etag = tag
		}
		if modified := resp.Header.Get("Last-Modified"); modified != "" {
			if _, err := http.ParseTime(modified); err == nil {
				s.modified = modified
			}
		}
	} else if total != s.Size || (s.etag != "" && resp.Header.Get("ETag") != s.etag) || (s.etag == "" && s.modified != "" && resp.Header.Get("Last-Modified") != s.modified) {
		return errors.New("Remote image changed; disconnect and reconnect it")
	}
	if _, err := io.ReadFull(resp.Body, target); err != nil {
		if s.ctx.Err() != nil {
			return s.ctx.Err()
		}
		return errors.New("Image server returned a short or interrupted block")
	}
	var extra [1]byte
	if _, err := io.ReadFull(resp.Body, extra[:]); err != io.EOF {
		if s.ctx.Err() != nil {
			return s.ctx.Err()
		}
		return errors.New("Image server returned an oversized or incomplete block")
	}
	return nil
}

func (s *HTTPSource) ReadBlock(offset uint64, length uint32) ([]byte, error) {
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
	if err := s.fetch(offset, s.buffer[:count], false); err != nil {
		return nil, err
	}
	s.start, s.valid = offset, int(count)
	return s.buffer[:length], nil
}

func (s *HTTPSource) Close() {
	s.cancel()
	s.transport.CloseIdleConnections()
}
