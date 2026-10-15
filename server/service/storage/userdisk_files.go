package storage

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"

	"NanoKVM-Server/proto"
)

var errUserDiskHost = errors.New("eject the user disk on the computer, then turn it off in USB settings")
var errUserDiskPath = errors.New("invalid user disk path")
var errUserDiskSpace = errors.New("not enough free space on the user disk, or file exceeds the FAT32 limit")

const maxUserDiskFile = int64(1<<32 - 1)

type userDiskEntry struct {
	Name       string `json:"name"`
	Directory  bool   `json:"directory"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modifiedAt"`
}

const userDiskPageSize = 200

type userDiskListing struct {
	Offset  int             `json:"offset"`
	Next    bool            `json:"next"`
	Path    string          `json:"path"`
	Entries []userDiskEntry `json:"entries"`
	Free    uint64          `json:"free"`
	Total   uint64          `json:"total"`
}

func userDiskPath(value string) (string, error) {
	if value == "" || value == "." {
		return ".", nil
	}
	if !utf8.ValidString(value) || strings.HasPrefix(value, "/") || len(value) > 4096 {
		return "", errUserDiskPath
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." || len(utf16.Encode([]rune(part))) > 255 ||
			strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") ||
			strings.ContainsAny(part, "\\:*?\"<>|") || strings.ContainsFunc(part, unicode.IsControl) {
			return "", errUserDiskPath
		}
	}
	return value, nil
}

func userDiskSpace(root *os.Root) (free, total uint64, err error) {
	dir, err := root.Open(".")
	if err != nil {
		return 0, 0, err
	}
	defer dir.Close()
	var stat unix.Statfs_t
	if err = unix.Fstatfs(int(dir.Fd()), &stat); err != nil {
		return 0, 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), stat.Blocks * uint64(stat.Bsize), nil
}

func listUserDisk(root *os.Root, name string, offset int) (userDiskListing, error) {
	result := userDiskListing{Path: name, Offset: offset, Entries: []userDiskEntry{}}
	dir, err := root.Open(name)
	if err != nil {
		return result, err
	}
	defer dir.Close()
	// Read bounded batches, including when skipping earlier pages.
	for remaining := offset; remaining > 0; {
		batch, skipErr := dir.ReadDir(min(remaining, userDiskPageSize))
		remaining -= len(batch)
		if skipErr != nil {
			if errors.Is(skipErr, io.EOF) {
				break
			}
			return result, skipErr
		}
	}
	entries, err := dir.ReadDir(userDiskPageSize)
	if err != nil && !errors.Is(err, io.EOF) {
		return result, err
	}
	more, err := dir.ReadDir(1)
	if err != nil && !errors.Is(err, io.EOF) {
		return result, err
	}
	result.Next = len(more) > 0
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return result, err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			continue
		}
		result.Entries = append(result.Entries, userDiskEntry{
			Name: entry.Name(), Directory: info.IsDir(), Size: info.Size(),
			ModifiedAt: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(result.Entries, func(i, j int) bool {
		a, b := result.Entries[i], result.Entries[j]
		if a.Directory != b.Directory {
			return a.Directory
		}
		return a.Name < b.Name
	})
	result.Free, result.Total, err = userDiskSpace(root)
	return result, err
}

// Stream into a temporary file on the user disk, never into server RAM or /tmp.
// Publish only a complete upload; an existing file is never overwritten.
func uploadUserDisk(root *os.Root, name string, body io.Reader, size int64) error {
	if name == "." {
		return errUserDiskPath
	}
	if _, err := root.Lstat(name); err == nil {
		return os.ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	free, _, err := userDiskSpace(root)
	if err != nil {
		return err
	}
	limit := int64(min(free, uint64(maxUserDiskFile)))
	if size > limit {
		return errUserDiskSpace
	}
	temp := path.Join(path.Dir(name), ".upload-"+rand.Text())
	file, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(temp)
	n, copyErr := io.Copy(file, io.LimitReader(body, limit+1))
	if copyErr == nil && (n > limit || (size >= 0 && n != size)) {
		if n > limit {
			copyErr = errUserDiskSpace
		} else {
			copyErr = io.ErrUnexpectedEOF
		}
	}
	if copyErr == nil {
		copyErr = file.Sync()
	}
	err = errors.Join(copyErr, file.Close())
	if err != nil {
		return err
	}
	// The whole mounted session has the exclusive disk lock. The PC cannot
	// write here and no other file operation can race the existence check.
	if _, err = root.Lstat(name); err == nil {
		return os.ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return root.Rename(temp, name)
}

func fileError(c *gin.Context, err error) {
	code, reason := -3, "io"
	switch {
	case errors.Is(err, errUserDiskHost):
		code, reason = -2, "host"
	case errors.Is(err, ErrUserDiskBusy):
		code, reason = -6, "busy"
	case errors.Is(err, errUserDiskPath):
		code, reason = -1, "path"
	// ENOTEMPTY also matches os.ErrExist on Unix; keep the specific reason first.
	case errors.Is(err, unix.ENOTEMPTY):
		reason = "notEmpty"
	case errors.Is(err, os.ErrExist):
		reason = "exists"
	case errors.Is(err, os.ErrNotExist):
		reason = "missing"
	case errors.Is(err, errUserDiskSpace), errors.Is(err, unix.ENOSPC):
		reason = "space"
	}
	log.Warnf("user disk files (%s): %v", reason, err)
	c.JSON(http.StatusOK, proto.Response{Code: code, Msg: reason})
}

func (s *Service) userDiskFiles(c *gin.Context, writable bool, action func(*os.Root, string) (any, error)) {
	name, err := userDiskPath(c.Query("path"))
	if err != nil {
		fileError(c, err)
		return
	}
	if !userDiskMu.TryLock() {
		fileError(c, ErrUserDiskBusy)
		return
	}
	defer userDiskMu.Unlock()
	if err = userDiskAvailable(); err != nil {
		fileError(c, err)
		return
	}
	root, closeDisk, err := mountUserDisk(writable)
	if err != nil {
		fileError(c, err)
		return
	}
	closed := false
	defer func() {
		if !closed {
			if err := closeDisk(); err != nil {
				log.Errorf("unmount user disk: %v", err)
			}
		}
	}()
	data, operationErr := action(root, name)
	closeErr := closeDisk()
	closed = true
	err = errors.Join(operationErr, closeErr)
	if err != nil {
		if !c.Writer.Written() {
			fileError(c, err)
		} else if errors.Is(err, os.ErrDeadlineExceeded) {
			log.Warnf("user disk transfer stalled: %v", err)
		} else {
			log.Errorf("user disk transfer: %v", err)
		}
		return
	}
	if !c.Writer.Written() {
		var rsp proto.Response
		rsp.OkRspWithData(c, data)
	}
}

func (s *Service) ListUserDiskFiles(c *gin.Context) {
	offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil || offset < 0 || offset > 1_000_000 {
		fileError(c, errUserDiskPath)
		return
	}
	c.Header("Cache-Control", "no-store")
	s.userDiskFiles(c, false, func(root *os.Root, name string) (any, error) { return listUserDisk(root, name, offset) })
}

// A transfer holds userDiskMu and the mount for as long as the client keeps
// it open, and the server sets no read or write timeout. A paused download or
// a vanished uploader would keep the disk from the computer, so each chunk
// renews a connection deadline: a transfer that keeps moving is never cut,
// one with no progress for userDiskStall fails and releases the disk.
var userDiskStall = 30 * time.Second

type stallReader struct {
	io.Reader
	rc *http.ResponseController
}

func (r stallReader) Read(p []byte) (int, error) {
	_ = r.rc.SetReadDeadline(time.Now().Add(userDiskStall))
	return r.Reader.Read(p)
}

type stallWriter struct {
	io.Writer
	rc *http.ResponseController
}

func (w stallWriter) Write(p []byte) (int, error) {
	_ = w.rc.SetWriteDeadline(time.Now().Add(userDiskStall))
	return w.Writer.Write(p)
}

// A writer wrapper without Unwrap() between gin and net/http would make the
// deadlines unsupported; say so rather than lose the guard silently.
func newStallReader(c *gin.Context) io.Reader {
	rc := http.NewResponseController(c.Writer)
	if err := rc.SetReadDeadline(time.Now().Add(userDiskStall)); err != nil {
		log.Warnf("user disk upload without a stall deadline: %v", err)
		return c.Request.Body
	}
	return stallReader{c.Request.Body, rc}
}

func newStallWriter(c *gin.Context) io.Writer {
	rc := http.NewResponseController(c.Writer)
	if err := rc.SetWriteDeadline(time.Now().Add(userDiskStall)); err != nil {
		log.Warnf("user disk download without a stall deadline: %v", err)
		return c.Writer
	}
	return stallWriter{c.Writer, rc}
}

func (s *Service) UploadUserDiskFile(c *gin.Context) {
	// The next request on the connection resets the read deadline.
	body := newStallReader(c)
	s.userDiskFiles(c, true, func(root *os.Root, name string) (any, error) {
		return nil, uploadUserDisk(root, name, body, c.Request.ContentLength)
	})
}
func (s *Service) MakeUserDiskDirectory(c *gin.Context) {
	s.userDiskFiles(c, true, func(root *os.Root, name string) (any, error) {
		if name == "." {
			return nil, errUserDiskPath
		}
		return nil, root.Mkdir(name, 0700)
	})
}
func (s *Service) DeleteUserDiskFile(c *gin.Context) {
	s.userDiskFiles(c, true, func(root *os.Root, name string) (any, error) {
		if name == "." {
			return nil, errUserDiskPath
		}
		return nil, root.Remove(name)
	})
}
func (s *Service) DownloadUserDiskFile(c *gin.Context) {
	s.userDiskFiles(c, false, func(root *os.Root, name string) (any, error) {
		file, err := root.Open(name)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, errUserDiskPath
		}
		c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(name)}))
		c.Header("Content-Type", "application/octet-stream")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Cache-Control", "no-store")
		c.Header("Content-Length", fmt.Sprint(info.Size()))
		c.Status(http.StatusOK)
		c.Writer.WriteHeaderNow()
		// net/http clears the write deadline once the response is finished.
		_, err = io.Copy(newStallWriter(c), file)
		return nil, err
	})
}
