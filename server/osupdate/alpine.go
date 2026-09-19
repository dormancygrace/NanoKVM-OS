package osupdate

// The Alpine attended path is deliberately independent from the signed .nkos
// updater. It accepts only the builder's fixed output set and hands the
// verified directory to nanokvm-stage-update.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const AlpineStateDir = "/data/.nanokvm-alpine"
const AlpineStageHelper = "/usr/sbin/nanokvm-stage-update"
const AlpineRebootCommand = "/sbin/reboot"

var alpineStateDir = AlpineStateDir
var alpineStageHelper = AlpineStageHelper
var alpineRebootCommand = AlpineRebootCommand

const (
	alpineMaxRequest  = 64 << 10
	alpineMaxResponse = 2 << 20
	alpineMaxPackages = 256
	alpineMaxText     = 4 << 20
)

var alpinePackageRE = regexp.MustCompile(`^[a-z0-9][a-z0-9+_.-]{0,127}$`)
var alpineBuildIDRE = regexp.MustCompile(`^[0-9a-f]{24}$`)
var alpineHashRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

var alpineFileLimits = map[string]int64{
	"SHA256SUMS":              64 << 10,
	"alpine-rootfs.tar.gz":    192 << 20,
	"boot-alpine.sd":          64 << 20,
	"boot-alpine-recovery.sd": 64 << 20,
	"recovery-manifest.json":  alpineMaxText,
	"request-manifest.txt":    alpineMaxText,
	"requested-packages.txt":  alpineMaxText,
	"installed-packages.txt":  alpineMaxText,
	"repositories":            alpineMaxText,
	"apk-world.txt":           alpineMaxText,
	"trusted-keys.sha256":     alpineMaxText,
}

var alpineRequiredFiles = map[string]bool{
	"alpine-rootfs.tar.gz": true, "boot-alpine.sd": true, "boot-alpine-recovery.sd": true,
	"recovery-manifest.json": true,
	"request-manifest.txt":   true, "requested-packages.txt": true, "installed-packages.txt": true,
	"repositories": true, "apk-world.txt": true, "trusted-keys.sha256": true,
}

type AlpineArtifact struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	URL    string `json:"url"`
	Size   int64  `json:"size,omitempty"`
}

type AlpineCurrent struct {
	Profile  string `json:"profile"`
	APKWorld string `json:"apk_world"`
}

type AlpineState struct {
	State    string           `json:"state"`
	Message  string           `json:"message,omitempty"`
	BuildID  string           `json:"build_id,omitempty"`
	Profile  string           `json:"profile,omitempty"`
	Packages []string         `json:"packages,omitempty"`
	Files    []AlpineArtifact `json:"files,omitempty"`
}

type AlpineBuildRequest struct {
	Profile  string   `json:"profile"`
	Packages []string `json:"packages"`
}

func currentAlpine() AlpineCurrent {
	profileBytes, _ := os.ReadFile("/etc/nanokvm-build-profile")
	worldBytes, _ := os.ReadFile("/etc/apk/world")
	return AlpineCurrent{Profile: strings.TrimSpace(string(profileBytes)), APKWorld: string(worldBytes)}
}

func GetAlpineCurrent() AlpineCurrent { return currentAlpine() }

func GetAlpineState() AlpineState {
	var state AlpineState
	if readJSON(filepath.Join(alpineStateDir, "state.json"), &state) != nil {
		state.State = "idle"
	}
	return state
}

func SetAlpineState(state AlpineState) error {
	if err := os.MkdirAll(alpineStateDir, 0700); err != nil {
		return err
	}
	return writeJSON(filepath.Join(alpineStateDir, "state.json"), state)
}

func validateAlpineRequest(req AlpineBuildRequest) error {
	if req.Profile != "stock" && req.Profile != "c906-scalar" {
		return errors.New("invalid Alpine profile")
	}
	if len(req.Packages) > alpineMaxPackages {
		return errors.New("too many APK packages")
	}
	seen := make(map[string]bool, len(req.Packages))
	for _, pkg := range req.Packages {
		if !alpinePackageRE.MatchString(pkg) {
			return fmt.Errorf("invalid APK package name: %s", pkg)
		}
		if seen[pkg] {
			return errors.New("duplicate APK package name")
		}
		seen[pkg] = true
	}
	return nil
}

func trustedBuilderURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && path.Clean(u.Path) != u.Path {
		return nil, errors.New("invalid Alpine builder URL")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, errors.New("Alpine builder URL must use HTTP or HTTPS")
	}
	return u, nil
}

func sameOriginRelative(base *url.URL, raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, "/") || strings.ContainsAny(u.Path, "\\\x00\r\n") || path.Clean(u.Path) != u.Path {
		return nil, errors.New("builder returned an unsafe artifact URL")
	}
	resolved := base.ResolveReference(u)
	if resolved.Scheme != base.Scheme || !strings.EqualFold(resolved.Host, base.Host) {
		return nil, errors.New("artifact URL escaped builder origin")
	}
	return resolved, nil
}

func validateAlpineArtifact(a AlpineArtifact) error {
	limit, ok := alpineFileLimits[a.Name]
	if !ok || !alpineHashRE.MatchString(a.SHA256) || a.Size < 0 || a.Size > limit {
		return errors.New("invalid Alpine artifact metadata")
	}
	return nil
}

func parseBuildResponse(base *url.URL, body io.Reader) (AlpineState, error) {
	var response struct {
		BuildID string           `json:"build_id"`
		Files   []AlpineArtifact `json:"files"`
	}
	decoder := json.NewDecoder(io.LimitReader(body, alpineMaxResponse))
	if err := decoder.Decode(&response); err != nil || !alpineBuildIDRE.MatchString(response.BuildID) {
		return AlpineState{}, errors.New("invalid Alpine builder response")
	}
	if len(response.Files) != len(alpineRequiredFiles) {
		return AlpineState{}, errors.New("invalid Alpine artifact list")
	}
	seen := make(map[string]bool, len(response.Files))
	for i := range response.Files {
		a := &response.Files[i]
		if seen[a.Name] || !alpineRequiredFiles[a.Name] {
			return AlpineState{}, errors.New("unexpected Alpine artifact")
		}
		if err := validateAlpineArtifact(*a); err != nil {
			return AlpineState{}, err
		}
		if _, err := sameOriginRelative(base, a.URL); err != nil {
			return AlpineState{}, err
		}
		seen[a.Name] = true
	}
	for name := range alpineRequiredFiles {
		if !seen[name] {
			return AlpineState{}, fmt.Errorf("builder omitted %s", name)
		}
	}
	sort.Slice(response.Files, func(i, j int) bool { return response.Files[i].Name < response.Files[j].Name })
	return AlpineState{State: "built", BuildID: response.BuildID, Files: response.Files}, nil
}

func BuildAlpine(ctx context.Context, builder string, req AlpineBuildRequest) (AlpineState, error) {
	if err := validateAlpineRequest(req); err != nil {
		return AlpineState{}, err
	}
	base, err := trustedBuilderURL(builder)
	if err != nil {
		return AlpineState{}, err
	}
	encoded, err := json.Marshal(req)
	if err != nil {
		return AlpineState{}, err
	}
	request, err := http.NewRequestWithContext(ctx, "POST", base.String()+"/v1/builds", strings.NewReader(string(encoded)))
	if err != nil {
		return AlpineState{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 30 * time.Minute, CheckRedirect: func(r *http.Request, _ []*http.Request) error {
		if r.URL.Scheme != base.Scheme || !strings.EqualFold(r.URL.Host, base.Host) {
			return errors.New("builder redirect escaped origin")
		}
		return nil
	}}
	response, err := client.Do(request)
	if err != nil {
		return AlpineState{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return AlpineState{}, fmt.Errorf("builder returned HTTP %d", response.StatusCode)
	}
	state, err := parseBuildResponse(base, response.Body)
	if err != nil {
		return AlpineState{}, err
	}
	state.Profile, state.Packages = req.Profile, append([]string(nil), req.Packages...)
	sort.Strings(state.Packages)
	return state, nil
}

func downloadAlpineFile(ctx context.Context, client *http.Client, base *url.URL, artifact AlpineArtifact, destination string) error {
	u, err := sameOriginRelative(base, artifact.URL)
	if err != nil {
		return err
	}
	limit := alpineFileLimits[artifact.Name]
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("artifact %s returned HTTP %d", artifact.Name, resp.StatusCode)
	}
	if resp.ContentLength > limit {
		return errors.New("Alpine artifact exceeds size limit")
	}
	f, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() { f.Close() }()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return err
	}
	if n > limit || (artifact.Size > 0 && n != artifact.Size) {
		return errors.New("Alpine artifact size mismatch")
	}
	if artifact.SHA256 != "" && hex.EncodeToString(h.Sum(nil)) != artifact.SHA256 {
		return fmt.Errorf("%s SHA256 mismatch", artifact.Name)
	}
	return f.Sync()
}

func verifyAlpineSums(dir string, names map[string]bool) error {
	b, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil || len(b) > 64<<10 {
		return errors.New("invalid SHA256SUMS")
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		parts := strings.Fields(line)
		if len(parts) != 2 || !alpineHashRE.MatchString(parts[0]) || !names[parts[1]] || seen[parts[1]] {
			return errors.New("invalid SHA256SUMS entry")
		}
		seen[parts[1]] = true
		got, e := HashFile(filepath.Join(dir, parts[1]))
		if e != nil || got != parts[0] {
			return errors.New("SHA256SUMS verification failed")
		}
	}
	if len(seen) != len(names) {
		return errors.New("SHA256SUMS is incomplete")
	}
	return nil
}

func StageAlpine(ctx context.Context, builder string, state AlpineState) error {
	if !alpineBuildIDRE.MatchString(state.BuildID) || len(state.Files) < len(alpineRequiredFiles) {
		return errors.New("no Alpine build is ready")
	}
	base, err := trustedBuilderURL(builder)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(alpineStateDir, 0700); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(alpineStateDir, "bundle-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	client := &http.Client{Timeout: 10 * time.Minute, CheckRedirect: func(r *http.Request, _ []*http.Request) error {
		if r.URL.Scheme != base.Scheme || !strings.EqualFold(r.URL.Host, base.Host) {
			return errors.New("artifact redirect escaped origin")
		}
		return nil
	}}
	names := make(map[string]bool, len(state.Files))
	for _, artifact := range state.Files {
		if err := validateAlpineArtifact(artifact); err != nil || names[artifact.Name] {
			return errors.New("invalid Alpine artifact state")
		}
		if err := downloadAlpineFile(ctx, client, base, artifact, filepath.Join(dir, artifact.Name)); err != nil {
			return err
		}
		names[artifact.Name] = true
	}
	for name := range alpineRequiredFiles {
		if !names[name] {
			return errors.New("Alpine artifact state is incomplete")
		}
	}
	sums := AlpineArtifact{Name: "SHA256SUMS", URL: "/artifacts/" + state.BuildID + "/SHA256SUMS"}
	if _, err := sameOriginRelative(base, sums.URL); err != nil {
		return err
	}
	if err := downloadAlpineFile(ctx, client, base, AlpineArtifact{Name: sums.Name, URL: sums.URL}, filepath.Join(dir, sums.Name)); err != nil {
		return err
	}
	// The generic downloader needs a digest, so fetch sums without a predeclared hash and verify its size.
	if info, e := os.Stat(filepath.Join(dir, sums.Name)); e != nil || info.Size() > 64<<10 {
		return errors.New("invalid SHA256SUMS size")
	}
	if err := verifyAlpineSums(dir, names); err != nil {
		return err
	}
	command := exec.CommandContext(ctx, alpineStageHelper, dir)
	if output, e := command.CombinedOutput(); e != nil {
		return fmt.Errorf("Alpine stage failed: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

func ActivateAlpine(ctx context.Context) error {
	if _, err := os.Stat(filepath.Join("/data", "nanokvm-update")); err != nil {
		return errors.New("no staged Alpine update")
	}
	if output, err := exec.CommandContext(ctx, alpineStageHelper, "--activate", filepath.Join("/data", "nanokvm-update")).CombinedOutput(); err != nil {
		return fmt.Errorf("Alpine activation failed: %s", strings.TrimSpace(string(output)))
	}
	if output, err := exec.CommandContext(ctx, alpineRebootCommand).CombinedOutput(); err != nil {
		return fmt.Errorf("reboot failed: %s", strings.TrimSpace(string(output)))
	}
	return nil
}
