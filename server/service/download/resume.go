package download

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	log "github.com/sirupsen/logrus"
)

// Runtime-only metadata survives an application restart, not a power loss.
// The partial image remains hidden from the mountable image list until complete.
const imageResumePath = "/run/nanokvm-download-resume.json"

type imageResume struct {
	URL      string `json:"url"`
	Path     string `json:"path"`
	Filename string `json:"filename"`
	SHA256   string `json:"sha256,omitempty"`
	ETag     string `json:"etag"`
	Total    int64  `json:"total"`
}

type imageContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *imageContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func strongETag(s string) bool {
	return len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' && !strings.ContainsAny(s, "\r\n")
}

func saveImageResume(path string, job *imageResume) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".nanokvm-resume-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(job)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func loadImageResume(path string) (*imageResume, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var job imageResume
	if err := json.NewDecoder(io.LimitReader(f, 16384)).Decode(&job); err != nil {
		return nil, err
	}
	u, err := url.Parse(job.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("invalid saved download URL")
	}
	if filepath.Dir(job.Path) != "/data" || !strings.HasPrefix(filepath.Base(job.Path), ".nanokvm-download-") || filepath.Clean(job.Path) != job.Path {
		return nil, errors.New("invalid saved partial image path")
	}
	if job.Filename == "" || job.Filename == "." || job.Filename == ".." || filepath.Base(job.Filename) != job.Filename {
		return nil, errors.New("invalid saved image filename")
	}
	if _, err := parseSHA256(job.SHA256); err != nil {
		return nil, err
	}
	if !strongETag(job.ETag) || job.Total <= 0 {
		return nil, errors.New("missing saved image validator")
	}
	return &job, nil
}

type imageHeaders struct {
	Status int
	Length int64
	ETag   string
	Start  int64
	Total  int64
}

func readImageHeaders(path string) imageHeaders {
	meta := imageHeaders{Length: -1, Start: -1, Total: -1}
	f, err := os.Open(path)
	if err != nil {
		return meta
	}
	defer f.Close()
	scan := bufio.NewScanner(io.LimitReader(f, 1024*1024))
	for scan.Scan() {
		line := scan.Text()
		if strings.HasPrefix(line, "HTTP/") {
			meta = imageHeaders{Length: -1, Start: -1, Total: -1}
			fields := strings.Fields(line)
			if len(fields) > 1 {
				meta.Status, _ = strconv.Atoi(fields[1])
			}
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(key) {
		case "etag":
			meta.ETag = value
		case "content-length":
			if n, err := strconv.ParseInt(value, 10, 64); err == nil && n >= 0 {
				meta.Length = n
			}
		case "content-range":
			var start, end, total int64
			if n, err := fmt.Sscanf(value, "bytes %d-%d/%d", &start, &end, &total); err == nil && n == 3 && start >= 0 && end >= start && total > end {
				meta.Start, meta.Total = start, total
			}
		}
	}
	return meta
}

func (s *Service) resumeImageDownload() {
	job, err := loadImageResume(imageResumePath)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		log.Warn("Saved image download cannot be resumed; partial file retained")
		_ = os.Remove(imageResumePath)
		return
	}
	// Never follow a replaced partial-file symlink into another filesystem path.
	f, err := os.OpenFile(job.Path, os.O_RDWR|syscall.O_NOFOLLOW, 0)
	if err != nil {
		log.Warn("Saved partial image is unavailable")
		return
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() >= job.Total {
		f.Close()
		log.Warn("Saved partial image has an invalid size; file retained")
		return
	}
	if _, err = f.Seek(0, io.SeekEnd); err != nil {
		f.Close()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done, err := s.beginDownload(job.URL, cancel)
	if err != nil {
		cancel()
		f.Close()
		return
	}
	s.setDownloadProgress(done, fmt.Sprintf("%.2f%%", float64(info.Size())/float64(job.Total)*100))
	go func() {
		defer cancel()
		expected, _ := hex.DecodeString(job.SHA256)
		if job.SHA256 == "" {
			expected = nil
		}
		err := downloadCurlTransfer(ctx, job.URL, f, expected, func(p string) { s.setDownloadProgress(done, p) }, job)
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Rename(job.Path, filepath.Join("/data", job.Filename))
		}
		// On an error retain the hidden partial file; never publish mixed data.
		if errors.Is(err, context.Canceled) {
			_ = os.Remove(job.Path)
		}
		_ = os.Remove(imageResumePath)
		s.completeImageDownload(done, err)
	}()
}

func (s *Service) completeImageDownload(done chan struct{}, err error) {
	status := downloadStatusSuccess
	if errors.Is(err, context.Canceled) {
		status = downloadStatusIdle
	} else if errors.Is(err, errSHA256Mismatch) {
		status = downloadStatusChecksumFailed
	} else if err != nil {
		status = downloadStatusFailed
		log.Errorf("Failed to download image: %v", err)
	}
	s.finishDownload(done, status)
}
