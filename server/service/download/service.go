package download

import (
	"NanoKVM-Server/internal/hwcrypto"
	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/storage"
	"NanoKVM-Server/utils"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

type downloadStatus string

const (
	downloadStatusIdle           downloadStatus = "idle"
	downloadStatusInProgress     downloadStatus = "in_progress"
	downloadStatusSuccess        downloadStatus = "success"
	downloadStatusFailed         downloadStatus = "failed"
	downloadStatusChecksumFailed downloadStatus = "checksum_failed"
)

var (
	transferSentinelPath  = utils.TransferSentinelPath
	errDownloadInProgress = errors.New("download in progress")
	errSHA256Mismatch     = errors.New("sha256 mismatch")
	imageDirectory        = "/data"
	validISOFilename      = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
)

type Service struct {
	downloadMutex      sync.Mutex
	downloadCancel     context.CancelFunc
	downloadDone       chan struct{}
	downloadStatus     downloadStatus
	downloadFile       string
	downloadPercentage string
	downloadProgress   transferProgress
}

func NewService() *Service {
	// The old process is gone; restore a validated partial download if present.
	_ = os.Remove(transferSentinelPath)
	s := &Service{downloadStatus: downloadStatusIdle}
	cleanupImageTransfers(imageDirectory, imageResumePath)
	s.resumeImageDownload()
	return s
}

func (s *Service) CancelDownloadImage(c *gin.Context) {
	var rsp proto.Response

	s.downloadMutex.Lock()
	cancel := s.downloadCancel
	done := s.downloadDone
	s.downloadMutex.Unlock()

	if cancel == nil || done == nil {
		rsp.ErrRsp(c, -1, "no cancellable download in progress")
		return
	}

	cancel()
	select {
	case <-done:
		rsp.OkRsp(c)
	case <-time.After(10 * time.Second):
		rsp.ErrRsp(c, -1, "cancel download timed out")
	}
}

func (s *Service) beginDownload(file string, cancel context.CancelFunc) (chan struct{}, error) {
	s.downloadMutex.Lock()
	defer s.downloadMutex.Unlock()

	if s.downloadDone != nil {
		return nil, errDownloadInProgress
	}

	lockFile, err := os.OpenFile(
		transferSentinelPath,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		0o644,
	)
	if err != nil {
		if os.IsExist(err) {
			return nil, errDownloadInProgress
		}
		return nil, fmt.Errorf("acquire transfer lock failed: %w", err)
	}
	if err := lockFile.Close(); err != nil {
		_ = os.Remove(transferSentinelPath)
		return nil, fmt.Errorf("close transfer lock failed: %w", err)
	}

	done := make(chan struct{})
	s.downloadCancel = cancel
	s.downloadDone = done
	s.downloadStatus = downloadStatusInProgress
	s.downloadFile = file
	s.downloadPercentage = ""
	s.downloadProgress = transferProgress{}

	return done, nil
}

func (s *Service) setDownloadFile(done chan struct{}, file string) {
	s.downloadMutex.Lock()
	defer s.downloadMutex.Unlock()

	if s.downloadDone == done {
		s.downloadFile = file
	}
}

func (s *Service) setDownloadProgress(done chan struct{}, progress transferProgress) {
	s.downloadMutex.Lock()
	defer s.downloadMutex.Unlock()

	if s.downloadDone == done {
		s.downloadPercentage = progress.percentage()
		s.downloadProgress = progress
	}
}

func (s *Service) finishDownload(done chan struct{}, status downloadStatus) {
	s.downloadMutex.Lock()
	if s.downloadDone != done {
		s.downloadMutex.Unlock()
		return
	}

	s.downloadCancel = nil
	s.downloadDone = nil
	s.downloadStatus = status
	s.downloadFile = ""
	s.downloadPercentage = ""
	s.downloadProgress = transferProgress{}
	_ = os.Remove(transferSentinelPath)
	s.downloadMutex.Unlock()

	close(done)
}

func (s *Service) ImageEnabled(c *gin.Context) {
	var rsp proto.Response

	testFile := "/data/.testfile"
	file, err := os.Create(testFile)
	if err != nil {
		rsp.OkRspWithData(c, &proto.ImageEnabledRsp{Enabled: false})
		return
	}
	defer file.Close()
	defer os.Remove(testFile)

	rsp.OkRspWithData(c, &proto.ImageEnabledRsp{Enabled: true})
}

func (s *Service) StatusImage(c *gin.Context) {
	var rsp proto.Response

	s.downloadMutex.Lock()
	status := s.downloadStatus
	file := s.downloadFile
	percentage := s.downloadPercentage
	progress := s.downloadProgress
	s.downloadMutex.Unlock()

	rsp.OkRspWithData(c, &proto.StatusImageRsp{
		Status:          string(status),
		File:            file,
		Percentage:      percentage,
		DownloadedBytes: progress.Bytes, TotalBytes: progress.Total, BytesPerSecond: progress.BytesPerSecond,
	})
}

func (s *Service) DownloadImageFile(c *gin.Context) {
	var rsp proto.Response

	log.Debug("DownloadImageFile")
	expectedSHA256, err := parseSHA256(c.GetHeader("X-SHA256-Sum"))
	if err != nil {
		rsp.ErrRsp(c, -1, err.Error())
		return
	}

	done, err := s.beginDownload("", nil)
	if err != nil {
		rsp.ErrRsp(c, -1, err.Error())
		return
	}
	finalStatus := downloadStatusFailed
	defer func() {
		s.finishDownload(done, finalStatus)
	}()

	dir, err := os.Open(imageDirectory)
	if err != nil {
		rsp.ErrRsp(c, -1, err.Error())
		return
	}
	budget, err := imageWriteBudget(dir)
	dir.Close()
	if err != nil {
		rsp.ErrRsp(c, -1, err.Error())
		return
	}
	// Leave room for multipart headers, not unlimited auxiliary form fields.
	requestLimit := budget
	if requestLimit < (1<<63-1)-(64<<10) {
		requestLimit += 64 << 10
	}
	if c.Request.ContentLength > requestLimit {
		rsp.ErrRsp(c, -1, errImageSpace.Error())
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, requestLimit)
	reader, err := c.Request.MultipartReader()
	if err != nil {
		log.Error("invalid multipart data")
		rsp.ErrRsp(c, -1, "invalid multipart data")
		return
	}

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Error("failed to read part")
			rsp.ErrRsp(c, -1, "failed to read part")
			return
		}
		if part.FormName() != "file" {
			_ = part.Close()
			continue
		}

		filename := part.FileName()
		if err := validateRemoteImageFilename(filename); err != nil {
			rsp.ErrRsp(c, -1, err.Error())
			return
		}
		s.setDownloadFile(done, filename)

		out, err := os.CreateTemp(imageDirectory, ".nanokvm-upload-*")
		if err != nil {
			log.Error("cannot create temporary file")
			rsp.ErrRsp(c, -1, "cannot create temporary file")
			return
		}
		tempPath := out.Name()
		defer os.Remove(tempPath)

		budget, err := imageWriteBudget(out)
		if err != nil {
			out.Close()
			rsp.ErrRsp(c, -1, err.Error())
			return
		}
		lw := newLoggingWriter(out, c.Request.ContentLength, func(progress transferProgress) {
			s.setDownloadProgress(done, progress)
		})
		_, copyErr := copyImageWithin(lw, part, expectedSHA256, budget)
		lw.stopTicker()
		var partCloseErr error
		if copyErr == nil {
			partCloseErr = part.Close()
		}
		outCloseErr := out.Close()
		if errors.Is(copyErr, errSHA256Mismatch) {
			finalStatus = downloadStatusChecksumFailed
			rsp.ErrRsp(c, -1, errSHA256Mismatch.Error())
			return
		}
		if copyErr != nil || partCloseErr != nil || outCloseErr != nil {
			log.Error("write failed")
			if copyErr != nil {
				rsp.ErrRsp(c, -1, copyErr.Error())
			} else {
				rsp.ErrRsp(c, -1, "write failed")
			}
			return
		}

		if err := validateUploadedImage(tempPath, filename); err != nil {
			rsp.ErrRsp(c, -1, err.Error())
			return
		}

		if err := installImage(imageDirectory, tempPath, filename); err != nil {
			rsp.ErrRsp(c, -1, err.Error())
			return
		}

		finalStatus = downloadStatusIdle
		rsp.OkRspWithData(c, &proto.StatusImageRsp{
			Status:     string(downloadStatusIdle),
			File:       "",
			Percentage: "",
		})
		return
	}

	rsp.ErrRsp(c, -1, "file is required")
}

// Browser uploads, URL downloads and resumptions use the same filename policy.
func validateRemoteImageFilename(filename string) error {
	return validateImageFilename(filename, ".iso", ".img")
}

func validateImageFilename(filename string, suffixes ...string) error {
	if filename == "" {
		return errors.New("no filename")
	}
	if filepath.Base(filename) != filename || strings.Contains(filename, "..") {
		return errors.New("invalid filename")
	}
	lower := strings.ToLower(filename)
	if !slices.ContainsFunc(suffixes, func(suffix string) bool { return strings.HasSuffix(lower, suffix) }) {
		return fmt.Errorf("only %s files allowed", strings.Join(suffixes, " or "))
	}
	if !validISOFilename.MatchString(filename) {
		return errors.New("invalid filename")
	}
	return nil
}

// installImage publishes a completed temporary file as filename in dir.
// Uploads, new downloads and resumed downloads all add images through it.
func installImage(dir, tempPath, filename string) error {
	if err := validateRemoteImageFilename(filename); err != nil {
		return err
	}
	destPath, err := utils.JoinWithin(dir, filename)
	if err != nil {
		return err
	}
	return storage.InstallImage(tempPath, destPath)
}

func (s *Service) DownloadImage(c *gin.Context) {
	var req proto.DownloadImageReq
	var rsp proto.Response

	log.Debug("DownloadImage")

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}

	expectedSHA256, err := parseSHA256(req.SHA256Sum)
	if err != nil {
		rsp.ErrRsp(c, -1, err.Error())
		return
	}

	u, err := url.Parse(req.File)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path == "" {
		rsp.ErrRsp(c, -1, "invalid url")
		return
	}
	// The name lands in /data; accept only the images the storage page lists.
	filename := filepath.Base(u.Path)
	if err := validateRemoteImageFilename(filename); err != nil {
		rsp.ErrRsp(c, -1, "invalid url: "+err.Error())
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	done, err := s.beginDownload(req.File, cancel)
	if err != nil {
		cancel()
		rsp.ErrRsp(c, -1, err.Error())
		return
	}

	go func() {
		defer cancel()

		if err := s.downloadRemoteImage(ctx, req.File, expectedSHA256, filename, func(progress transferProgress) {
			s.setDownloadProgress(done, progress)
		}); err != nil {
			if errors.Is(err, context.Canceled) {
				log.Debug("Image download canceled")
				s.finishDownload(done, downloadStatusIdle)
				return
			}

			log.Errorf("Failed to download image: %v", err)
			status := downloadStatusFailed
			if errors.Is(err, errSHA256Mismatch) {
				status = downloadStatusChecksumFailed
			}
			s.finishDownload(done, status)
			return
		}
		s.finishDownload(done, downloadStatusSuccess)
	}()

	rsp.OkRspWithData(c, &proto.StatusImageRsp{
		Status:     string(downloadStatusInProgress),
		File:       req.File,
		Percentage: "",
	})
}

func parseSHA256(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}

	sum, err := hex.DecodeString(value)
	if err != nil || len(sum) != sha256.Size {
		return nil, errors.New("invalid sha256sum")
	}

	return sum, nil
}

func (s *Service) downloadRemoteImage(
	ctx context.Context,
	rawURL string,
	expectedSHA256 []byte,
	filename string,
	onProgress func(transferProgress),
) error {
	tempFile, err := os.CreateTemp(imageDirectory, ".nanokvm-download-*")
	if err != nil {
		return fmt.Errorf("create temporary image failed: %w", err)
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)
	var copyErr error
	if _, err := os.Stat(imageCurlPath); err == nil {
		job := &imageResume{URL: rawURL, Path: tempPath, Filename: filename, SHA256: hex.EncodeToString(expectedSHA256)}
		// A checkpoint is optional for downloading, but required for resumption.
		_ = saveImageResume(imageResumePath, job)
		defer os.Remove(imageResumePath)
		copyErr = downloadCurlTransfer(ctx, rawURL, tempFile, expectedSHA256, onProgress, job)
	} else {
		copyErr = downloadGoImage(ctx, rawURL, tempFile, expectedSHA256, onProgress)
	}
	closeErr := tempFile.Close()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return fmt.Errorf("close downloaded image failed: %w", closeErr)
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	if err := installImage(imageDirectory, tempPath, filename); err != nil {
		return fmt.Errorf("install downloaded image failed: %w", err)
	}

	return nil
}

// Keep host development and bases without curl functional. Release firmware
// ships curl/OpenSSL and takes the native TLS path above.
func downloadGoImage(ctx context.Context, rawURL string, dst *os.File, expected []byte, progress func(transferProgress)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download response status %d", resp.StatusCode)
	}
	budget, err := imageWriteBudget(dst)
	if err != nil {
		return err
	}
	if resp.ContentLength > budget {
		return errImageSpace
	}
	lw := newLoggingWriter(dst, resp.ContentLength, progress)
	defer lw.stopTicker()
	n, err := copyImageWithin(lw, resp.Body, expected, budget)
	if err == nil && resp.ContentLength >= 0 && n != resp.ContentLength {
		return io.ErrUnexpectedEOF
	}
	return err
}

// copyImage computes SHA-256 only when the caller supplied a checksum to verify.
// Images without a checksum previously paid the full hashing cost and discarded
// the result, which is expensive for multi-gigabyte images on the C906 CPU.
func copyImage(dst io.Writer, src io.Reader, expected []byte) (int64, error) {
	if expected == nil {
		return io.Copy(dst, src)
	}
	hasher := hwcrypto.NewSHA256()
	n, err := io.Copy(io.MultiWriter(dst, hasher), src)
	if err != nil {
		return n, err
	}
	if !bytes.Equal(hasher.Sum(nil), expected) {
		return n, errSHA256Mismatch
	}
	return n, nil
}

type loggingWriter struct {
	writer     io.Writer
	total      atomic.Int64
	totalSize  int64
	ticker     *time.Ticker
	meter      *transferMeter
	stopped    chan struct{}
	done       chan struct{}
	stopOnce   sync.Once
	onProgress func(transferProgress)
}

func newLoggingWriter(writer io.Writer, totalSize int64, onProgress func(transferProgress)) *loggingWriter {
	lw := &loggingWriter{
		writer:     writer,
		totalSize:  totalSize,
		onProgress: onProgress,
		meter:      newTransferMeter(0),
	}
	lw.startTicker()
	return lw
}

func (lw *loggingWriter) startTicker() {
	lw.ticker = time.NewTicker(time.Second)
	lw.done = make(chan struct{})
	lw.stopped = make(chan struct{})
	go func() {
		defer close(lw.stopped)
		for {
			select {
			case <-lw.done:
				return
			case <-lw.ticker.C:
				lw.updateProgress()
			}
		}
	}()
}

func (lw *loggingWriter) stopTicker() {
	if lw == nil {
		return
	}
	lw.stopOnce.Do(func() {
		lw.ticker.Stop()
		close(lw.done)
		<-lw.stopped
	})
}

func (lw *loggingWriter) updateProgress() {
	if lw.onProgress != nil {
		lw.onProgress(lw.meter.sample(lw.total.Load(), lw.totalSize, time.Now()))
	}
}

func (lw *loggingWriter) Write(p []byte) (int, error) {
	n, err := lw.writer.Write(p)
	lw.total.Add(int64(n))
	return n, err
}
