package acme

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/internal/decompress"
	"NanoKVM-Server/utils"
)

// A release tag: dotted numbers, as acme.sh tags them (3.1.6).
var releaseTag = regexp.MustCompile(`^[0-9]+(\.[0-9]+){1,3}$`)

// Release is the latest stable release as last seen on GitHub.
type Release struct {
	Tag       string `json:"tag"`
	CheckedAt string `json:"checkedAt"`
}

var (
	latestMu     sync.Mutex
	latestKnown  *Release
	latestClient = &http.Client{Timeout: 30 * time.Second}
	latestURL    = latestReleaseURL
	tarballBase  = tarballURLPrefix
)

// latestRelease asks GitHub for the latest stable release and remembers it.
func latestRelease(ctx context.Context) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "NanoKVM-OS")
	rsp, err := latestClient.Do(req)
	if err != nil {
		return nil, errors.New("GitHub is not reachable")
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub answered %d", rsp.StatusCode)
	}
	var body struct {
		TagName    string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(io.LimitReader(rsp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, errors.New("unexpected answer from GitHub")
	}
	tag := strings.TrimPrefix(strings.TrimSpace(body.TagName), "v")
	if body.Draft || body.Prerelease || !releaseTag.MatchString(tag) {
		return nil, fmt.Errorf("unexpected release %q", body.TagName)
	}
	release := &Release{Tag: tag, CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	latestMu.Lock()
	latestKnown = release
	latestMu.Unlock()
	return release, nil
}

func knownLatest() *Release {
	latestMu.Lock()
	defer latestMu.Unlock()
	if latestKnown == nil {
		return nil
	}
	r := *latestKnown
	return &r
}

// newerVersion reports whether candidate is a later release than installed
// (numeric, part by part; an unknown installed version counts as older).
func newerVersion(installed, candidate string) bool {
	if !releaseTag.MatchString(candidate) {
		return false
	}
	if !releaseTag.MatchString(installed) {
		return true
	}
	a, b := strings.Split(installed, "."), strings.Split(candidate, ".")
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y int
		if i < len(a) {
			x, _ = strconv.Atoi(a[i])
		}
		if i < len(b) {
			y, _ = strconv.Atoi(b[i])
		}
		if x != y {
			return y > x
		}
	}
	return false
}

// Install puts the latest stable release in place (a first install or an
// update). data/ is created when missing and never touched otherwise, so an
// update keeps the account and the domain configuration.
func Install(ctx context.Context) (*Release, error) {
	opMu.Lock()
	defer opMu.Unlock()
	if running() {
		return nil, errors.New("an acme.sh operation is running")
	}
	release, err := latestRelease(ctx)
	if err != nil {
		return nil, fmt.Errorf("find the latest acme.sh release: %w", err)
	}
	if err := installTag(ctx, release.Tag, tarballBase+release.Tag+".tar.gz"); err != nil {
		return nil, err
	}
	return release, nil
}

// installTag downloads the source archive of a tag over TLS, extracts the
// script and the DNS hooks into a staging directory next to bin/, checks
// that the script carries the tag's version and swaps the directory in.
func installTag(ctx context.Context, tag, url string) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	tarball := filepath.Join(root, "download.tar.gz")
	_ = os.Remove(tarball)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if _, err := utils.Download(req, tarball, maxTarball, nil); err != nil {
		log.Errorf("download acme.sh %s from %s: %v", tag, url, err)
		return fmt.Errorf("download acme.sh %s: GitHub is not reachable", tag)
	}
	defer os.Remove(tarball)

	staging := filepath.Join(root, "bin.staging")
	_ = os.RemoveAll(staging)
	if err := extract(tarball, staging, tag); err != nil {
		_ = os.RemoveAll(staging)
		return fmt.Errorf("unpack acme.sh %s: %w", tag, err)
	}
	data, err := os.ReadFile(filepath.Join(staging, "acme.sh"))
	if err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	if got := scriptVersion(data); got != tag {
		_ = os.RemoveAll(staging)
		return fmt.Errorf("the downloaded script says version %q, the release is %s", got, tag)
	}
	old := filepath.Join(root, "bin.old")
	_ = os.RemoveAll(old)
	if _, err := os.Stat(binDir()); err == nil {
		if err := os.Rename(binDir(), old); err != nil {
			_ = os.RemoveAll(staging)
			return err
		}
	}
	if err := os.Rename(staging, binDir()); err != nil {
		_ = os.Rename(old, binDir())
		_ = os.RemoveAll(staging)
		return err
	}
	_ = os.RemoveAll(old)
	if err := os.MkdirAll(dataDir(), 0o700); err != nil {
		return err
	}
	log.Infof("acme.sh %s installed in %s", tag, binDir())
	return nil
}

// extract writes the allowed regular files of the archive under dest with
// the archive prefix removed: acme.sh 0755, the hooks 0644. Only the script
// and the DNS hooks are installed; deploy/notify hooks, documentation and CI
// files of the archive stay out.
func extract(tarball, dest, tag string) error {
	allowed := regexp.MustCompile(`^acme\.sh-` + regexp.QuoteMeta(tag) + `/(acme\.sh|dnsapi/dns_[a-z0-9_]+\.sh)$`)
	f, err := os.Open(tarball)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := decompress.NewGzipReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	if err := os.MkdirAll(filepath.Join(dest, "dnsapi"), 0o755); err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	hooks := 0
	hasScript := false
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if !allowed.MatchString(header.Name) {
			continue
		}
		if header.Typeflag != tar.TypeReg {
			return fmt.Errorf("%s is not a regular file", header.Name)
		}
		if header.Size > maxFile {
			return fmt.Errorf("%s is larger than %d bytes", header.Name, maxFile)
		}
		rel := header.Name[strings.Index(header.Name, "/")+1:]
		mode := os.FileMode(0o644)
		if rel == "acme.sh" {
			mode = 0o755
			hasScript = true
		} else {
			hooks++
		}
		if err := writeFile(filepath.Join(dest, rel), tr, header.Size, mode); err != nil {
			return err
		}
	}
	if !hasScript || hooks == 0 {
		return errors.New("the archive holds no acme.sh script or no DNS hooks")
	}
	return nil
}

func writeFile(path string, r io.Reader, size int64, mode os.FileMode) error {
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	written, err := io.Copy(out, io.LimitReader(r, size))
	if err == nil && written != size {
		err = io.ErrUnexpectedEOF
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	return err
}
