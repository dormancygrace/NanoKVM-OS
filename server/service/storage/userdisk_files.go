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
	case errors.Is(err, os.ErrExist):
		reason = "exists"
	case errors.Is(err, os.ErrNotExist):
		reason = "missing"
	case errors.Is(err, errUserDiskSpace), errors.Is(err, unix.ENOSPC):
		reason = "space"
	case errors.Is(err, unix.ENOTEMPTY):
		reason = "notEmpty"
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
func (s *Service) UploadUserDiskFile(c *gin.Context) {
	s.userDiskFiles(c, true, func(root *os.Root, name string) (any, error) {
		return nil, uploadUserDisk(root, name, c.Request.Body, c.Request.ContentLength)
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
		_, err = io.Copy(c.Writer, file)
		return nil, err
	})
}
