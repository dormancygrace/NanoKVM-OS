package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Network boot uses the official releases as published: the iPXE CD images
// (one of them signed for UEFI Secure Boot) and the netboot.xyz menus of one
// release. Both come from the latest GitHub release; every iPXE file is
// checked against the SHA-256 digest GitHub publishes for it.

const (
	ipxeImage   = "ipxe.iso"           // BIOS and UEFI, every architecture, no Secure Boot
	ipxeSBImage = "ipxe-x86_64-sb.iso" // x86-64 UEFI, signed for Secure Boot
	// The images are a few MiB; the cap only stops a runaway transfer.
	ipxeMaxBytes = 64 << 20
)

var (
	netbootDir         = "/data/.nanokvm/netboot"
	githubAPI          = "https://api.github.com"
	ipxeDownloadBase   = "https://github.com/ipxe/ipxe/releases/download"
	netbootxyzMenuBase = "https://boot.netboot.xyz"
	netbootHTTP        = &http.Client{Timeout: 10 * time.Minute}

	ipxeTag       = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
	netbootxyzTag = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	sha256Digest  = regexp.MustCompile(`^sha256:([0-9a-f]{64})$`)
)

// netbootState is what /data/.nanokvm/netboot/state.json records.
type netbootState struct {
	IPXE       string            `json:"ipxe"`       // installed iPXE release tag
	IPXEFiles  map[string]string `json:"ipxeFiles"`  // image name -> SHA-256
	Netbootxyz string            `json:"netbootxyz"` // netboot.xyz release used for its menus
	CheckedAt  string            `json:"checkedAt"`
	SecureBoot bool              `json:"secureBoot"`
	// SharingByNetboot records that enabling network boot turned USB
	// internet sharing on, so that turning it off (or a reboot that ended
	// network boot) restores the user's own setting.
	SharingByNetboot bool `json:"sharingByNetboot"`
}

func netbootStatePath() string { return filepath.Join(netbootDir, "state.json") }

func loadNetbootState() (netbootState, error) {
	var st netbootState
	data, err := os.ReadFile(netbootStatePath())
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err = json.Unmarshal(data, &st); err != nil {
		return netbootState{}, fmt.Errorf("read network boot state: %w", err)
	}
	if st.IPXE != "" && !ipxeTag.MatchString(st.IPXE) {
		st.IPXE, st.IPXEFiles = "", nil
	}
	if st.Netbootxyz != "" && !netbootxyzTag.MatchString(st.Netbootxyz) {
		st.Netbootxyz = ""
	}
	return st, nil
}

func saveNetbootState(st netbootState) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(netbootStatePath(), append(data, '\n'))
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".netboot-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// ipxeImagePath is where a verified image of an installed release lives.
func ipxeImagePath(tag, name string) string {
	return filepath.Join(netbootDir, "ipxe-"+tag, name)
}

type githubAsset struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

type githubRelease struct {
	Tag    string
	Assets []githubAsset
}

// latestGitHubRelease returns the latest stable release of owner/repo.
func latestGitHubRelease(ctx context.Context, repo string) (githubRelease, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubAPI+"/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return githubRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "NanoKVM-OS")
	rsp, err := netbootHTTP.Do(req)
	if err != nil {
		return githubRelease{}, errors.New("GitHub is not reachable")
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return githubRelease{}, fmt.Errorf("GitHub answered %d for %s", rsp.StatusCode, repo)
	}
	var body struct {
		TagName    string        `json:"tag_name"`
		Draft      bool          `json:"draft"`
		Prerelease bool          `json:"prerelease"`
		Assets     []githubAsset `json:"assets"`
	}
	if err = json.NewDecoder(io.LimitReader(rsp.Body, 4<<20)).Decode(&body); err != nil {
		return githubRelease{}, fmt.Errorf("unexpected answer from GitHub for %s", repo)
	}
	if body.Draft || body.Prerelease {
		return githubRelease{}, fmt.Errorf("%s: the latest release is not a stable release", repo)
	}
	return githubRelease{Tag: strings.TrimSpace(body.TagName), Assets: body.Assets}, nil
}

// ipxeDigests picks the two images from an iPXE release and their digests.
func ipxeDigests(rel githubRelease) (map[string]string, error) {
	if !ipxeTag.MatchString(rel.Tag) {
		return nil, fmt.Errorf("unexpected iPXE release %q", rel.Tag)
	}
	want := map[string]string{}
	for _, a := range rel.Assets {
		if a.Name != ipxeImage && a.Name != ipxeSBImage {
			continue
		}
		m := sha256Digest.FindStringSubmatch(a.Digest)
		if m == nil {
			return nil, fmt.Errorf("iPXE %s: GitHub publishes no SHA-256 for %s", rel.Tag, a.Name)
		}
		if a.Size <= 0 || a.Size > ipxeMaxBytes {
			return nil, fmt.Errorf("iPXE %s: unexpected size of %s", rel.Tag, a.Name)
		}
		want[a.Name] = m[1]
	}
	for _, name := range []string{ipxeImage, ipxeSBImage} {
		if want[name] == "" {
			return nil, fmt.Errorf("iPXE %s has no %s", rel.Tag, name)
		}
	}
	return want, nil
}

// downloadVerified fetches url into path, accepting it only when its SHA-256
// equals digest. A partial or mismatching file never takes path's place.
func downloadVerified(ctx context.Context, url, path, digest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "NanoKVM-OS")
	rsp, err := netbootHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", filepath.Base(path), err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: server answered %d", filepath.Base(path), rsp.StatusCode)
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".download-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(rsp.Body, ipxeMaxBytes+1))
	if err == nil && n > ipxeMaxBytes {
		err = fmt.Errorf("download %s: larger than %d bytes", filepath.Base(path), ipxeMaxBytes)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != digest {
		return fmt.Errorf("download %s: SHA-256 %s does not match the published %s", filepath.Base(path), got, digest)
	}
	return os.Rename(f.Name(), path)
}

// verifyFile re-checks an installed image before it goes on the virtual disk.
func verifyFile(path, digest string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != digest {
		return fmt.Errorf("%s is damaged; update network boot to download it again", filepath.Base(path))
	}
	return nil
}

// netbootxyzAvailable checks that boot.netboot.xyz serves the menus of tag.
func netbootxyzAvailable(ctx context.Context, tag string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, netbootxyzMenuBase+"/"+tag+"/menu.ipxe", nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "NanoKVM-OS")
	rsp, err := netbootHTTP.Do(req)
	if err != nil {
		return errors.New("boot.netboot.xyz is not reachable")
	}
	defer rsp.Body.Close()
	head, _ := io.ReadAll(io.LimitReader(rsp.Body, 64))
	if rsp.StatusCode != http.StatusOK || !strings.HasPrefix(string(head), "#!ipxe") {
		return fmt.Errorf("boot.netboot.xyz does not serve the %s menus", tag)
	}
	return nil
}

// updateNetbootReleases installs the latest iPXE images and selects the
// latest netboot.xyz release. It returns the new state and keeps the old iPXE
// files: callers remove them (removeOldIPXE) once nothing has them mounted.
func updateNetbootReleases(ctx context.Context, st netbootState) (netbootState, error) {
	ipxeRel, err := latestGitHubRelease(ctx, "ipxe/ipxe")
	if err != nil {
		return st, err
	}
	digests, err := ipxeDigests(ipxeRel)
	if err != nil {
		return st, err
	}
	xyzRel, err := latestGitHubRelease(ctx, "netbootxyz/netboot.xyz")
	if err != nil {
		return st, err
	}
	if !netbootxyzTag.MatchString(xyzRel.Tag) {
		return st, fmt.Errorf("unexpected netboot.xyz release %q", xyzRel.Tag)
	}
	if err = netbootxyzAvailable(ctx, xyzRel.Tag); err != nil {
		return st, err
	}

	next := st
	next.Netbootxyz = xyzRel.Tag
	next.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	same := st.IPXE == ipxeRel.Tag && len(st.IPXEFiles) == len(digests)
	for name, digest := range digests {
		same = same && st.IPXEFiles[name] == digest && verifyFile(ipxeImagePath(ipxeRel.Tag, name), digest) == nil
	}
	if !same {
		for name, digest := range digests {
			url := ipxeDownloadBase + "/" + ipxeRel.Tag + "/" + name
			if err = downloadVerified(ctx, url, ipxeImagePath(ipxeRel.Tag, name), digest); err != nil {
				return st, err
			}
		}
		next.IPXE = ipxeRel.Tag
		next.IPXEFiles = digests
	}
	if err = saveNetbootState(next); err != nil {
		return st, err
	}
	return next, nil
}

// removeOldIPXE deletes the image directories of other releases.
func removeOldIPXE(keep string) {
	entries, err := os.ReadDir(netbootDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "ipxe-") && e.Name() != "ipxe-"+keep {
			_ = os.RemoveAll(filepath.Join(netbootDir, e.Name()))
		}
	}
}
