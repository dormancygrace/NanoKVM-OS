// Package toolcache keeps what a program printed, such as its version, until
// the program file is replaced, so that status polls do not start the same
// program every few seconds.
package toolcache

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Cache runs a command once per executable file. Package upgrades replace
// the file (a new inode), which starts the command again. Callers asking at
// the same time share one run.
type Cache struct {
	mu      sync.Mutex
	run     func(path string, args ...string) (string, error)
	retry   time.Duration
	now     func() time.Time
	entries map[string]entry
}

type entry struct {
	stamp  string
	output string
	err    error
	at     time.Time
}

// New returns a cache over run. A failed run is answered from the cache for
// retry only, so a timeout under load does not stick.
func New(run func(path string, args ...string) (string, error), retry time.Duration) *Cache {
	return &Cache{run: run, retry: retry, now: time.Now, entries: map[string]entry{}}
}

// Output returns the output and error of run(path, args...) from the last
// run on the same file. A file that cannot be examined is run every time.
func (c *Cache) Output(path string, args ...string) (string, error) {
	key := strings.Join(append([]string{path}, args...), "\x00")
	stamp := fileStamp(path)
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if e, ok := c.entries[key]; ok && stamp != "" && e.stamp == stamp && (e.err == nil || now.Sub(e.at) < c.retry) {
		return e.output, e.err
	}
	output, err := c.run(path, args...)
	c.entries[key] = entry{stamp: stamp, output: output, err: err, at: now}
	return output, err
}

// fileStamp identifies the file at path, following symbolic links.
func fileStamp(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	var inode uint64
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		inode = st.Ino
	}
	return fmt.Sprintf("%d:%d:%d", inode, info.Size(), info.ModTime().UnixNano())
}
