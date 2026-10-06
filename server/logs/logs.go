// Package logs provides bounded, read-only snapshots for the administrator GUI.
package logs

import (
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const (
	MaxBytes           = 1024 * 1024
	MaxLines           = 20000
	maxCachedSnapshots = 3
	RefreshInterval    = 5 * time.Second
)

type Snapshot struct {
	ArchiveAvailable bool   `json:"archiveAvailable"`
	Boot             string `json:"boot"`
	Source           string `json:"source"`
	State            string `json:"state"`
	Content          string `json:"content"`
	Lines            int    `json:"lines"`
	Truncated        bool   `json:"truncated"`
	CollectedAt      int64  `json:"collectedAt"`
}

var ErrSource = errors.New("unsupported log source")
var ErrReadRate = errors.New("log read rate exceeded")

// Application retains only the most recent formatted logrus output in RAM.
// It introduces no file writes and follows the configured logger level.
var Application = &Buffer{}

// Buffer is a fixed-size ring. Write never blocks on file or network I/O.
type Buffer struct {
	mu        sync.Mutex
	data      [MaxBytes]byte
	next      int
	size      int
	truncated bool
}

func (b *Buffer) Write(data []byte) (int, error) {
	n := len(data)
	b.mu.Lock()
	defer b.mu.Unlock()
	if n > MaxBytes {
		data = data[n-MaxBytes:]
	}
	if b.size+n > MaxBytes {
		b.truncated = true
	}
	first := copy(b.data[b.next:], data)
	copy(b.data[:], data[first:])
	b.next = (b.next + len(data)) % MaxBytes
	b.size = min(MaxBytes, b.size+n)
	return n, nil
}

func (b *Buffer) tail() ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data := make([]byte, b.size)
	start := (b.next - b.size + MaxBytes) % MaxBytes
	first := copy(data, b.data[start:])
	copy(data[first:], b.data[:])
	return data, b.truncated
}

// Collector serializes reads and shares a five-second cache across all clients.
// The client supplies a source ID, never a filename, command or byte limit.
type cachedLog struct {
	snapshot  Snapshot
	at        time.Time
	rawDigest [sha256.Size]byte
	truncated bool
	rawValid  bool
}

type Collector struct {
	mu          sync.Mutex
	cache       map[string]cachedLog
	misses      []time.Time
	now         func() time.Time
	logDir      string
	application *Buffer
	archive     *Archive
	kernel      func([]byte) (int, error)
}

func NewCollector() *Collector {
	return &Collector{
		cache:       make(map[string]cachedLog),
		now:         time.Now,
		logDir:      "/var/log",
		application: Application,
		kernel: func(data []byte) (int, error) {
			// SYSLOG_ACTION_READ_ALL reads without clearing the kernel ring.
			return unix.Klogctl(3, data)
		},
	}
}

var Default = NewCollector()

func (c *Collector) Read(source string) (Snapshot, error) {
	return c.ReadBoot(source, "current")
}

func (c *Collector) ReadBoot(source, boot string) (Snapshot, error) {
	return c.read(source, boot, false)
}

func (c *Collector) read(source, boot string, internal bool) (Snapshot, error) {
	if boot != "current" && boot != "previous" && boot != "older" {
		return Snapshot{}, ErrSource
	}
	switch source {
	case "system", "kernel", "application":
	default:
		return Snapshot{}, ErrSource
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	key := boot + "/" + source
	cached, hasCache := c.cache[key]
	if hasCache && !internal && now.Sub(cached.at) < RefreshInterval {
		return cached.snapshot, nil
	}
	// Six cache misses per five seconds is shared across authenticated clients.
	// Cache eviction or cycling boot/source IDs cannot remove this I/O bound.
	if !internal {
		first := 0
		for first < len(c.misses) && now.Sub(c.misses[first]) >= RefreshInterval {
			first++
		}
		c.misses = c.misses[first:]
		if len(c.misses) >= 6 {
			return Snapshot{}, ErrReadRate
		}
		c.misses = append(c.misses, now)
	}
	snapshot := Snapshot{Boot: boot, Source: source, State: "unavailable", CollectedAt: now.UnixMilli()}
	if boot != "current" {
		if c.archive != nil {
			if saved, err := c.archive.Read(boot, source); err == nil {
				snapshot = saved
			}
		}
		c.remember(key, snapshot, now, [sha256.Size]byte{}, false, false)
		return snapshot, nil
	}
	var data []byte
	var truncated bool
	var err error
	switch source {
	case "system":
		data, truncated, err = readSystemTail(c.logDir)
	case "application":
		data, truncated = c.application.tail()
	case "kernel":
		data = make([]byte, MaxBytes)
		var n int
		n, err = c.kernel(data)
		if err == nil {
			data = data[:n]
			// A full read may begin halfway through a kernel entry.
			truncated = n == MaxBytes
		}
	}
	var rawDigest [sha256.Size]byte
	if err == nil {
		rawDigest = sha256.Sum256(data)
		if hasCache && cached.rawValid && cached.rawDigest == rawDigest && cached.truncated == truncated {
			// Internal archive flushes bypass the TTL but still reuse the
			// already-redacted content after fresh source I/O.
			snapshot = cached.snapshot
			snapshot.CollectedAt = now.UnixMilli()
		} else {
			snapshot.Content, snapshot.Lines, snapshot.Truncated = sanitizeTail(data, truncated)
			snapshot.State = "ready"
			if snapshot.Content == "" {
				snapshot.State = "empty"
			}
		}
	}
	// Cache failures too: an absent or unreadable source cannot trigger a read
	// storm. No raw OS error (which may contain paths) is exposed.
	c.remember(key, snapshot, now, rawDigest, truncated, err == nil)
	return snapshot, nil
}

func readSystemTail(directory string) ([]byte, bool, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, false, err
	}
	defer root.Close()
	remaining := MaxBytes
	var parts [][]byte
	truncated := false
	found := false
	// BusyBox rotates messages into messages.0 (and messages.1 when configured).
	// Read newest files first, sharing one total byte budget, then present them
	// chronologically. The client never supplies any of these fixed names.
	for _, name := range []string{"messages", "messages.0", "messages.1"} {
		data, clipped, err := readFileTail(root, name, int64(remaining))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, false, err
		}
		found = true
		parts = append(parts, data)
		remaining -= len(data)
		truncated = truncated || clipped
		if remaining == 0 {
			break
		}
	}
	if !found {
		return nil, false, os.ErrNotExist
	}
	combined := make([]byte, 0, MaxBytes-remaining)
	for index := len(parts) - 1; index >= 0; index-- {
		combined = append(combined, parts[index]...)
	}
	return combined, truncated, nil
}

func readFileTail(root *os.Root, name string, limit int64) ([]byte, bool, error) {
	// Reject links; OpenRoot confinement and inode checks also cover path
	// replacement races. NONBLOCK prevents an accidental FIFO from blocking.
	before, err := root.Lstat(name)
	if err != nil {
		return nil, false, err
	}
	if !before.Mode().IsRegular() {
		return nil, false, errors.New("log is not a regular file")
	}
	file, err := root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(before, info) {
		return nil, false, errors.New("log file changed")
	}
	size := info.Size()
	length := min(size, limit)
	data := make([]byte, int(length))
	n, err := file.ReadAt(data, size-length)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, false, err
	}
	return data[:n], size > length, nil
}

func trimPartialLine(data []byte) []byte {
	if index := strings.IndexByte(string(data), '\n'); index >= 0 {
		return data[index+1:]
	}
	return nil
}

// Keep a MiB viewer from retaining all nine source/boot combinations in RAM.
func (c *Collector) remember(key string, snapshot Snapshot, now time.Time, rawDigest [sha256.Size]byte, truncated, rawValid bool) {
	if _, exists := c.cache[key]; !exists && len(c.cache) >= maxCachedSnapshots {
		oldestKey := ""
		var oldest time.Time
		for name, value := range c.cache {
			if oldestKey == "" || value.at.Before(oldest) {
				oldestKey, oldest = name, value.at
			}
		}
		delete(c.cache, oldestKey)
	}
	c.cache[key] = cachedLog{
		snapshot:  snapshot,
		at:        now,
		rawDigest: rawDigest,
		truncated: truncated,
		rawValid:  rawValid,
	}
}
