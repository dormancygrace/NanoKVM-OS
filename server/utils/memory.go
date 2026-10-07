package utils

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"
)

const GoMemLimitFile = "/etc/kvm/GOMEMLIMIT"
const minGoMemLimitMiB int64 = 50

// Without a saved setting the server limits the memory the Go runtime manages
// to a quarter of MemTotal: 26 MiB in the 4K video memory mode, where Linux
// has about 104 MiB. The whole server process normally has 11 MiB of
// anonymous memory (Go and C together) and peaked at about 22 MiB, so the
// limit does not make the collector work in normal operation; it makes it
// collect harder instead of letting the heap grow into the memory that apk and
// the rest of the system need. A GOMEMLIMIT environment variable takes the
// place of this default.
const defaultGoMemLimitDivisor = 4

// Serialize persistence and application so concurrent API requests cannot leave
// the process using a different limit from the one saved for the next start.
type goMemoryLimitStore struct {
	mutex sync.Mutex
	path  string
	apply func(int64) int64
	// fallback applies without a saved setting; zero until InitGoMemLimit.
	fallback int64
	memInfo  string
}

var goMemLimit = goMemoryLimitStore{path: GoMemLimitFile, apply: debug.SetMemoryLimit, memInfo: "/proc/meminfo"}

// defaultLimit is the GOMEMLIMIT environment value the runtime started with,
// otherwise a quarter of MemTotal, otherwise no limit.
func (s *goMemoryLimitStore) defaultLimit() int64 {
	if current := s.apply(-1); current != math.MaxInt64 {
		return current
	}
	data, err := os.ReadFile(s.memInfo)
	if err != nil {
		return math.MaxInt64
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			if kib, err := strconv.ParseInt(fields[1], 10, 64); err == nil && kib > 0 {
				return kib * 1024 / defaultGoMemLimitDivisor
			}
		}
	}
	return math.MaxInt64
}

func normalizeGoMemLimit(limit int64) (int64, error) {
	if limit < 0 || limit > math.MaxInt64/(1024*1024) {
		return 0, fmt.Errorf("invalid Go memory limit in MiB: %d", limit)
	}
	return max(limit, minGoMemLimitMiB), nil
}

func (s *goMemoryLimitStore) read() (int64, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return 0, err
	}
	limit, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, err
	}
	return normalizeGoMemLimit(limit)
}

// InitGoMemLimit applies the saved limit, or the default without one. It runs
// once at start, before anything else changes the runtime limit.
func InitGoMemLimit() {
	goMemLimit.init()
}

func (s *goMemoryLimitStore) init() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.fallback = s.defaultLimit()
	limit, err := s.read()
	if os.IsNotExist(err) {
		s.apply(s.fallback)
		if s.fallback != math.MaxInt64 {
			log.Infof("GOMEMLIMIT %d MiB (default)", s.fallback/(1024*1024))
		}
		return
	}
	if err != nil {
		log.Errorf("failed to read GOMEMLIMIT: %s", err)
		s.apply(s.fallback)
		return
	}
	s.apply(limit * 1024 * 1024)
	log.Infof("GOMEMLIMIT %d MiB (saved)", limit)
}

func (s *goMemoryLimitStore) set(limit int64) error {
	limit, err := normalizeGoMemLimit(limit)
	if err != nil {
		return err
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Rename within /etc/kvm: neither readers nor the next boot should see a
	// truncated setting. Leave the running limit alone if persistence fails.
	file, err := os.CreateTemp(filepath.Dir(s.path), ".GOMEMLIMIT-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(0o644); err == nil {
		_, err = fmt.Fprintf(file, "%d\n", limit)
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(file.Name(), s.path); err != nil {
		return err
	}
	s.apply(limit * 1024 * 1024)
	return nil
}

func SetGoMemLimit(limit int64) error {
	return goMemLimit.set(limit)
}

func GetGoMemLimit() (int64, error) {
	goMemLimit.mutex.Lock()
	defer goMemLimit.mutex.Unlock()
	return goMemLimit.read()
}

func (s *goMemoryLimitStore) remove() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	if s.fallback == 0 {
		s.apply(math.MaxInt64)
	} else {
		s.apply(s.fallback)
	}
	return nil
}

func DelGoMemLimit() error {
	return goMemLimit.remove()
}

func IsGoMemLimitExist() bool {
	goMemLimit.mutex.Lock()
	defer goMemLimit.mutex.Unlock()
	_, err := os.Stat(goMemLimit.path)
	return err == nil
}
