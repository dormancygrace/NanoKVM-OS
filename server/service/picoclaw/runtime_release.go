package picoclaw

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const picoclawArchiveName = "picoclaw_Linux_riscv64.tar.gz"

type picoclawRelease struct {
	Tag         string
	ArchiveURL  string
	ChecksumURL string
}

// Resolve the official latest link once. Pin both downloads to that release,
// even if upstream publishes another release while installation is running.
func latestPicoclawRelease(ctx context.Context) (picoclawRelease, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, picoclawDownloadURL, nil)
	if err != nil {
		return picoclawRelease{}, err
	}
	client := &http.Client{
		Timeout:       picoclawDownloadTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return picoclawRelease{}, fmt.Errorf("cannot resolve latest PicoClaw release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusTemporaryRedirect && resp.StatusCode != http.StatusMovedPermanently {
		return picoclawRelease{}, fmt.Errorf("cannot resolve latest PicoClaw release: %s", resp.Status)
	}
	return selectPicoclawRelease(resp.Header.Get("Location"))
}

func selectPicoclawRelease(location string) (picoclawRelease, error) {
	result := picoclawRelease{}
	u, err := url.Parse(location)
	const prefix = "/sipeed/picoclaw/releases/download/"
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || !strings.HasPrefix(u.Path, prefix) || u.RawQuery != "" || u.Fragment != "" {
		return result, fmt.Errorf("unexpected PicoClaw release URL")
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, prefix), "/")
	if len(parts) != 2 || parts[1] != picoclawArchiveName || !strings.HasPrefix(parts[0], "v") || len(parts[0]) < 2 || strings.ContainsAny(parts[0], "\\?#") {
		return result, fmt.Errorf("invalid PicoClaw release asset")
	}
	result.Tag = parts[0]
	result.ArchiveURL = location
	result.ChecksumURL = "https://github.com" + prefix + result.Tag + "/picoclaw_" + strings.TrimPrefix(result.Tag, "v") + "_checksums.txt"
	return result, nil
}
