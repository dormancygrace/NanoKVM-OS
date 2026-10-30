package vm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"NanoKVM-Server/logs"
	"NanoKVM-Server/proto"
	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

// The developer archive (Diagnostics > Prepare archive for developers). The
// collector script of nanokvm-base gathers logs, system, hardware, network
// and allow-listed settings into a RAM directory; every file then passes the
// log viewer's sanitizer and address masking before it is packed.
var (
	devArchiveScript  = "/usr/libexec/nanokvm/dev-archive"
	devArchiveTimeout = 90 * time.Second
	devArchiveLimit   = 16 << 20
	// The cleaned text of all files together, bounded before compression:
	// gzip output says little about the memory the texts take.
	devArchiveRaw  = 24 << 20
	devArchiveFile = 1 << 20
	devArchiveBusy sync.Mutex
)

// Screen content never leaves the device: saved frames and screenshots are
// refused by name, whatever the collector picked up.
var devArchiveFrame = regexp.MustCompile(`(?i)(screenshot|snapshot|frame-dump|\.(bgr|rgb|yuv|nv12|jpe?g|png|bmp|raw|h26[45]))$`)

// Settings files parsed as JSON or YAML before cleaning.
var devArchiveStructured = regexp.MustCompile(`^config/.*\.(json|ya?ml)$`)

// Settings keys whose value, or whole subtree, is or names a credential:
// secretKey, turnCred, turnUser, apiKey, password, a TLS key... Durations
// and switches such as refreshTokenDuration or revokeTokensOnLogout stay.
var devArchiveSecretKey = regexp.MustCompile(`(?i)(cred|pass|secret|private|cookie|api_?key|authorization|bearer)|(^|_)(key|token|user|username|login)$|[a-z0-9](key|token|user)$`)

const devArchiveReadme = `NanoKVM OS developer archive

Collected on the device by %s, created %s.

Contents: logs of the last three boots, the kernel and apk; system state
(versions, processes, memory, mounts, services, packages); hardware (video
capture, HDMI receiver diagnostics, USB gadget, thermal); network state;
allow-listed settings; diagnostics.json, the report of the Diagnostics page.

Before packing, every file was cleaned on the device:
- IPv4, IPv6 and MAC addresses and Wi-Fi network names are masked;
- entries that may hold passwords, keys, tokens or cookies are hidden;
- JSON and YAML settings are parsed and the values of keys naming
  credentials, user names or keys are hidden with everything under them
  (comments are dropped); a settings file that does not parse is omitted.
Not collected at all: passwords, keys and certificates, the web login
secret, Wi-Fi and VPN configuration, user scripts, the USB serial number,
screen or video content and the contents of files on the user disk (the
names of images attached as virtual disks may appear).
Not masked: host names and web user names in logs.
%s`

func (s *Service) DownloadDevArchive(c *gin.Context) {
	var rsp proto.Response
	if !devArchiveBusy.TryLock() {
		rsp.ErrRsp(c, -1, "a developer archive is already being prepared")
		return
	}
	defer devArchiveBusy.Unlock()
	if _, err := os.Stat(devArchiveScript); err != nil {
		rsp.ErrRsp(c, -1, "update nanokvm-base to prepare a developer archive")
		return
	}
	dir, err := os.MkdirTemp("", "nanokvm-dev-archive-")
	if err != nil {
		rsp.ErrRsp(c, -1, "cannot prepare the developer archive")
		return
	}
	defer os.RemoveAll(dir)

	ctx, cancel := context.WithTimeout(c.Request.Context(), devArchiveTimeout)
	defer cancel()
	// A partial collection is still useful: pack what exists and say why.
	note := ""
	if _, err = diagnosticRun(ctx, 64<<10, devArchiveScript, "collect", dir); err != nil {
		note = fmt.Sprintf("\nThe collection did not finish: %v\n", err)
	}
	var report any
	if snapshot, e := readDiagnostics(c.Request.Context()); e == nil {
		report = diagnosticsReport(snapshot)
	}
	data, err := packDevArchive(dir, report, note, time.Now())
	if err != nil {
		rsp.ErrRsp(c, -1, err.Error())
		return
	}
	name := "nanokvm-dev-archive-" + time.Now().Format("20060102-150405") + ".tar.gz"
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Disposition", `attachment; filename="`+name+`"`)
	c.Data(http.StatusOK, "application/gzip", data)
}

func packDevArchive(dir string, report any, note string, now time.Time) ([]byte, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && entry.Type().IsRegular() {
			files = append(files, path)
		}
		return err
	})
	if err != nil {
		return nil, errors.New("cannot read the collected files")
	}
	sort.Strings(files)

	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	tw := tar.NewWriter(zw)
	raw := 0
	add := func(name, text string) error {
		raw += len(text)
		if raw > devArchiveRaw {
			return errors.New("the developer archive is too large")
		}
		if err := tw.WriteHeader(&tar.Header{Name: "nanokvm-dev-archive/" + name, Mode: 0o644, Size: int64(len(text)), ModTime: now, Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err := tw.Write([]byte(text))
		if err == nil && out.Len() > devArchiveLimit {
			err = errors.New("the developer archive is too large")
		}
		return err
	}
	var omitted []string
	for _, path := range files {
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		if devArchiveFrame.MatchString(rel) {
			omitted = append(omitted, rel)
			continue
		}
		data, err := readLimited(path, devArchiveFile)
		if err != nil || binaryData(data) {
			omitted = append(omitted, rel)
			continue
		}
		// Text with stray NUL bytes: the device-tree model ends in one, and
		// apk's log carries a few.
		data = bytes.ReplaceAll(data, []byte{0}, nil)
		if devArchiveStructured.MatchString(rel) {
			// Structured settings are parsed, not matched line by line: a
			// flow mapping or a block scalar would hide a credential from a
			// line rule. KEY=value and one-value files keep the line rule.
			if data, err = redactSettings(data); err != nil {
				omitted = append(omitted, rel)
				continue
			}
			// Already cleaned by key: the line rule would hide whole lines
			// such as refreshTokenDuration.
			if err = add(rel, logs.MaskAddresses(string(data))); err != nil {
				return nil, err
			}
			continue
		}
		if err = add(rel, logs.SanitizeFile(data)); err != nil {
			return nil, err
		}
	}
	if report != nil {
		b, err := json.MarshalIndent(report, "", "  ")
		if err == nil {
			err = add("diagnostics.json", string(b)+"\n")
		}
		if err != nil {
			return nil, err
		}
	}
	if len(omitted) > 0 {
		note += "\nOmitted as binary or unreadable: " + strings.Join(omitted, ", ") + "\n"
	}
	collector := "the NanoKVM OS collector"
	if b, err := os.ReadFile(filepath.Join(dir, "collector")); err == nil {
		collector = strings.TrimSpace(string(b))
	}
	if err = add("README.txt", fmt.Sprintf(devArchiveReadme, collector, now.Format(time.RFC3339), note)); err != nil {
		return nil, err
	}
	if err = tw.Close(); err == nil {
		err = zw.Close()
	}
	if err != nil {
		return nil, err
	}
	if out.Len() > devArchiveLimit {
		return nil, errors.New("the developer archive is too large")
	}
	return out.Bytes(), nil
}

// binaryData: more than one byte in twenty is a control character other than
// tab, newline or carriage return.
func binaryData(data []byte) bool {
	control := 0
	for _, b := range data {
		if b < 0x20 && b != '\t' && b != '\n' && b != '\r' {
			control++
		}
	}
	return control*20 > len(data)
}

// readLimited reads at most the last limit bytes of a file, without reading
// the rest: the collector already bounds its outputs, this bounds the memory.
func readLimited(path string, limit int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if size := info.Size(); size > int64(limit) {
		if _, err = f.Seek(size-int64(limit), io.SeekStart); err != nil {
			return nil, err
		}
	}
	return io.ReadAll(io.LimitReader(f, int64(limit)))
}

// redactSettings parses a JSON or YAML settings file and replaces the value
// of every key that names a credential, with all of its subtree. The file is
// written back without comments; a file that does not parse is refused.
func redactSettings(data []byte) ([]byte, error) {
	if json.Valid(data) {
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			return nil, err
		}
		var out bytes.Buffer
		enc := json.NewEncoder(&out)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		err := enc.Encode(redactValue(value))
		return out.Bytes(), err
	}
	var value any
	if err := yaml.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return yaml.Marshal(redactValue(value))
}

func redactValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if devArchiveSecretKey.MatchString(key) {
				v[key] = "<hidden>"
			} else {
				v[key] = redactValue(child)
			}
		}
	case map[any]any:
		for key, child := range v {
			if devArchiveSecretKey.MatchString(fmt.Sprint(key)) {
				v[key] = "<hidden>"
			} else {
				v[key] = redactValue(child)
			}
		}
	case []any:
		for i, child := range v {
			v[i] = redactValue(child)
		}
	}
	return value
}
