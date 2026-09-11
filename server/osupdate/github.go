package osupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const Repository = "dormancygrace/NanoKVM-OS"

type Release struct {
	Version  string `json:"version"`
	URL      string `json:"url"`
	AssetURL string `json:"asset_url"`
	Bytes    int64  `json:"bytes"`
}
type CheckState struct {
	CheckedAt string   `json:"checked_at"`
	Error     string   `json:"error"`
	Release   *Release `json:"release"`
	Checking  bool     `json:"checking"`
}

var checkMu sync.Mutex
var checkState CheckState

func GetCheck() CheckState { checkMu.Lock(); defer checkMu.Unlock(); return checkState }
func githubClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 || req.URL.Scheme != "https" || req.URL.User != nil {
			return errors.New("unsafe GitHub redirect")
		}
		h := req.URL.Hostname()
		if h != "github.com" && h != "api.github.com" && h != "release-assets.githubusercontent.com" && h != "objects.githubusercontent.com" {
			return errors.New("unexpected release download host")
		}
		return nil
	}}
}
func Check(ctx context.Context) {
	checkMu.Lock()
	if checkState.Checking {
		checkMu.Unlock()
		return
	}
	checkState.Checking = true
	checkMu.Unlock()
	result := CheckState{CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	defer func() { checkMu.Lock(); checkState = result; checkMu.Unlock() }()
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/repos/"+Repository+"/releases?per_page=10", nil)
	if err != nil {
		result.Error = err.Error()
		return
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "NanoKVM-OS-Updater/2")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := githubClient(20 * time.Second).Do(req)
	if err != nil {
		result.Error = "Could not reach GitHub"
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		result.Error = "Repository is private or unavailable. Signed file uploads remain available."
		return
	}
	if resp.StatusCode != 200 {
		result.Error = fmt.Sprintf("GitHub returned HTTP %d", resp.StatusCode)
		return
	}
	var releases []struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		URL        string `json:"html_url"`
		Assets     []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&releases); err != nil {
		result.Error = "Invalid GitHub release response"
		return
	}
	beta := strings.Contains(GetInstalled().Version, "-")
	for _, r := range releases {
		version := strings.TrimPrefix(r.Tag, "v")
		if r.Draft || (!beta && r.Prerelease) || !versionRE.MatchString(version) {
			continue
		}
		for _, a := range r.Assets {
			if (a.Name != "NanoKVM-OS-update.nkos" && a.Name != "NanoKVM-OS-application.nkos") || a.Size < 1 || a.Size > MaxBundle {
				continue
			}
			if !ValidAssetURL(a.URL) {
				continue
			}
			result.Release = &Release{Version: version, URL: "https://github.com/" + Repository + "/releases/tag/" + r.Tag, AssetURL: a.URL, Bytes: a.Size}
			return
		}
	}
}
func ValidAssetURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == "github.com" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && strings.HasPrefix(u.Path, "/"+Repository+"/releases/download/") && (strings.HasSuffix(u.Path, "/NanoKVM-OS-application.nkos") || strings.HasSuffix(u.Path, "/NanoKVM-OS-update.nkos"))
}
func Download(ctx context.Context, r Release, out io.Writer) error {
	if !ValidAssetURL(r.AssetURL) {
		return errors.New("unsupported release source")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", r.AssetURL, nil)
	if err != nil {
		return err
	}
	resp, err := githubClient(10 * time.Minute).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("release download returned HTTP %d", resp.StatusCode)
	}
	n, err := io.Copy(out, io.LimitReader(resp.Body, MaxBundle+1))
	if err != nil {
		return err
	}
	if n > MaxBundle || n != r.Bytes {
		return errors.New("release download size mismatch")
	}
	return nil
}

// Automatic discovery never downloads or installs an update without user action.
func RunChecks(ctx context.Context) {
	timer := time.NewTimer(3 * time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			Check(ctx)
			timer.Reset(24 * time.Hour)
		}
	}
}
