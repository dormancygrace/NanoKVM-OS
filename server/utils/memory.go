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
const defaultGoMemLimitBytes int64 = 1024 * 1024 * 1024

// Serialize persistence and application so concurrent API requests cannot leave
// the process using a different limit from the one saved for the next start.
type goMemoryLimitStore struct {
	mutex sync.Mutex
	path  string
	apply func(int64) int64
}

var goMemLimit = goMemoryLimitStore{path: GoMemLimitFile, apply: debug.SetMemoryLimit}

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

func InitGoMemLimit() {
	goMemLimit.mutex.Lock()
	defer goMemLimit.mutex.Unlock()
	limit, err := goMemLimit.read()
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		log.Errorf("failed to read GOMEMLIMIT: %s", err)
		return
	}
	goMemLimit.apply(limit * 1024 * 1024)
	log.Debugf("set GOMEMLIMIT to %d MiB", limit)
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
	s.apply(defaultGoMemLimitBytes)
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
