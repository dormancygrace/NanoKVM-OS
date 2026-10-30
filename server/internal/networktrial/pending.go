// Package networktrial shares the network trial's persistent record and
// background-work lifetime with shutdown preflight without importing services.
package networktrial

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
)

const RecordPath = "/boot/net-trial.json"

var ErrPending = errors.New("a network trial or rollback is still pending")
var workers atomic.Int64

// Begin keeps a trial busy through confirmation or the last successful
// rollback/sync. The returned release is idempotent. In-memory tracking also
// covers a failed removal sync, when the record may already be absent.
func Begin() func() {
	workers.Add(1)
	return sync.OnceFunc(func() { workers.Add(-1) })
}

// CheckIdle is called after HTTP mutation admission closes, which excludes
// new trials. It also refuses orphan records and unknown filesystem errors.
func CheckIdle(recordPath string) error {
	if workers.Load() != 0 {
		return ErrPending
	}
	_, err := os.Lstat(recordPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check network trial %s: %w", recordPath, err)
	}
	return fmt.Errorf("%w (%s)", ErrPending, recordPath)
}
