package logs

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const ArchiveInterval = time.Minute

var sources = []string{"system", "kernel", "application"}
var bootSlots = []string{"current", "previous", "older"}
var validBootID = regexp.MustCompile(`^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$`)

type BootInfo struct {
	ID        string `json:"id"`
	StartedAt int64  `json:"startedAt"`
	SavedAt   int64  `json:"savedAt"`
}

type archiveSource struct {
	Lines       int   `json:"lines"`
	Truncated   bool  `json:"truncated"`
	CollectedAt int64 `json:"collectedAt"`
}

type bootRecord struct {
	BootID    string                   `json:"bootId"`
	StartedAt int64                    `json:"startedAt"`
	SavedAt   int64                    `json:"savedAt"`
	Sources   map[string]archiveSource `json:"sources"`
}

// Archive owns three fixed directories on the persistent Alpine filesystem.
// Only sanitized snapshots reach disk. Each source occupies at most one MiB.
type Archive struct {
	mu      sync.Mutex
	flushMu sync.Mutex
	root    *os.Root
	record  bootRecord
	digests map[string][32]byte
	failed  bool
}

func NewArchive(directory, bootID string, startedAt int64) (*Archive, error) {
	if !validBootID.MatchString(bootID) {
		return nil, errors.New("invalid boot identifier")
	}
	if err := os.Mkdir(directory, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("invalid archive directory")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			root.Close()
		}
	}()
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("archive directory changed")
	}
	if err = root.Chmod(".", 0700); err != nil {
		return nil, err
	}
	a := &Archive{root: root, digests: map[string][32]byte{}}
	current, err := a.loadRecord("current")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil && current.BootID != bootID {
		if err = a.removeSlot("older"); err != nil {
			return nil, err
		}
		if err = a.moveSlot("previous", "older"); err != nil {
			return nil, err
		}
		if err = a.moveSlot("current", "previous"); err != nil {
			return nil, err
		}
		current = bootRecord{}
	}
	if err = root.Mkdir("current", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	if current.BootID == "" {
		current = bootRecord{BootID: bootID, StartedAt: startedAt, Sources: map[string]archiveSource{}}
	}
	a.record = current
	meta, err := json.Marshal(current)
	if err != nil {
		return nil, err
	}
	if err = a.atomicWrite("current/boot.json", meta); err != nil {
		return nil, err
	}
	directoryFile, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	err = directoryFile.Sync()
	directoryFile.Close()
	if err != nil {
		return nil, err
	}
	ok = true
	return a, nil
}

func (a *Archive) Close() error { return a.root.Close() }

func (a *Archive) loadRecord(slot string) (bootRecord, error) {
	var record bootRecord
	info, err := a.root.Lstat(slot)
	if err != nil {
		return record, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return record, errors.New("invalid boot directory")
	}
	data, err := readArchiveFile(a.root, slot+"/boot.json", 4096)
	if err != nil {
		return record, err
	}
	err = json.Unmarshal(data, &record)
	if err != nil || !validBootID.MatchString(record.BootID) || record.Sources == nil {
		return bootRecord{}, errors.New("invalid boot record")
	}
	return record, nil
}

func (a *Archive) moveSlot(from, to string) error {
	info, err := a.root.Lstat(from)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid boot directory")
	}
	return a.root.Rename(from, to)
}

func (a *Archive) removeSlot(slot string) error {
	info, err := a.root.Lstat(slot)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid boot directory")
	}
	for _, name := range append([]string{"boot.json", "boot.json.tmp"}, sourceNames()...) {
		err = a.root.Remove(slot + "/" + name)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return a.root.Remove(slot)
}
func sourceNames() []string {
	var names []string
	for _, source := range sources {
		names = append(names, source+".log", source+".log.tmp")
	}
	return names
}

func (a *Archive) atomicWrite(name string, data []byte) error {
	temporary := name + ".tmp"
	if err := a.root.Remove(temporary); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := a.root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer a.root.Remove(temporary)
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = a.root.Rename(temporary, name); err != nil {
		return err
	}
	// Persist the rename as well as the file data on a normal shutdown.
	directory, err := a.root.Open(filepath.Dir(name))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func readArchiveFile(root *os.Root, name string, limit int64) ([]byte, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > limit {
		return nil, errors.New("invalid archive file")
	}
	file, err := root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(before, info) {
		return nil, errors.New("archive file changed")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if len(data) > int(limit) {
		return nil, errors.New("archive size limit")
	}
	return data, err
}

func (a *Archive) Boots() []BootInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	boots := []BootInfo{}
	for _, slot := range bootSlots {
		if record, err := a.loadRecord(slot); err == nil {
			boots = append(boots, BootInfo{ID: slot, StartedAt: record.StartedAt, SavedAt: record.SavedAt})
		}
	}
	return boots
}

func (a *Archive) Read(slot, source string) (Snapshot, error) {
	if slot != "current" && slot != "previous" && slot != "older" {
		return Snapshot{}, ErrSource
	}
	valid := false
	for _, name := range sources {
		if name == source {
			valid = true
		}
	}
	if !valid {
		return Snapshot{}, ErrSource
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	record, err := a.loadRecord(slot)
	if err != nil {
		return Snapshot{}, err
	}
	meta, ok := record.Sources[source]
	if !ok {
		return Snapshot{}, os.ErrNotExist
	}
	data, err := readArchiveFile(a.root, slot+"/"+source+".log", MaxBytes)
	if err != nil {
		return Snapshot{}, err
	}
	// Apply the same output policy again to defend against modified archives.
	content, lines, clipped := sanitizeTail(append(data, '\n'), false)
	state := "ready"
	if content == "" {
		state = "empty"
	}
	return Snapshot{Source: source, Boot: slot, State: state, Content: content, Lines: lines, Truncated: meta.Truncated || clipped, CollectedAt: meta.CollectedAt}, nil
}

func (a *Archive) Save(snapshots []Snapshot) (saveErr error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	defer func() { a.failed = saveErr != nil }()
	record := a.record
	record.Sources = make(map[string]archiveSource)
	for source, meta := range a.record.Sources {
		record.Sources[source] = meta
	}
	digests := make(map[string][32]byte)
	for source, digest := range a.digests {
		digests[source] = digest
	}
	changed := false
	for _, snapshot := range snapshots {
		if snapshot.State == "unavailable" {
			continue
		}
		if len(snapshot.Content) > MaxBytes {
			return errors.New("archive size limit")
		}
		valid := false
		for _, source := range sources {
			if snapshot.Source == source {
				valid = true
			}
		}
		if !valid || snapshot.Boot != "current" {
			return ErrSource
		}
		digest := sha256.Sum256([]byte(snapshot.Content))
		if previous, ok := a.digests[snapshot.Source]; ok && previous == digest {
			continue
		}
		if err := a.atomicWrite("current/"+snapshot.Source+".log", []byte(snapshot.Content)); err != nil {
			return err
		}
		digests[snapshot.Source] = digest
		record.Sources[snapshot.Source] = archiveSource{snapshot.Lines, snapshot.Truncated, snapshot.CollectedAt}
		record.SavedAt = snapshot.CollectedAt
		changed = true
	}
	if !changed {
		return nil
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err = a.atomicWrite("current/boot.json", data); err != nil {
		return err
	}
	a.record, a.digests = record, digests
	return nil
}

func (c *Collector) Boots() ([]BootInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.archive == nil {
		return []BootInfo{{ID: "current"}}, false
	}
	return c.archive.Boots(), c.archive.Healthy()
}

func InitArchives() error {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return err
	}
	startedAt := time.Now()
	if uptime, err := os.ReadFile("/proc/uptime"); err == nil {
		fields := strings.Fields(string(uptime))
		if len(fields) > 0 {
			if seconds, err := strconv.ParseFloat(fields[0], 64); err == nil && seconds >= 0 {
				startedAt = startedAt.Add(-time.Duration(seconds * float64(time.Second)))
			}
		}
	}
	archive, err := NewArchive("/var/log/nanokvm-boots", strings.TrimSpace(string(data)), startedAt.UnixMilli())
	if err != nil {
		return err
	}
	Default.mu.Lock()
	Default.archive = archive
	Default.mu.Unlock()
	// Preserve application messages across service restarts in the same boot.
	if snapshot, err := archive.Read("current", "application"); err == nil && snapshot.Content != "" {
		_, _ = Application.Write([]byte(snapshot.Content + "\n"))
	}
	return nil
}

func FlushArchives() error {
	Default.mu.Lock()
	archive := Default.archive
	Default.mu.Unlock()
	if archive == nil {
		return nil
	}
	archive.flushMu.Lock()
	defer archive.flushMu.Unlock()
	// Internal reads bypass the client TTL but retain the current-source
	// digest, allowing unchanged sanitized output to be reused after fresh I/O.
	var snapshots []Snapshot
	for _, source := range sources {
		snapshot, err := Default.read(source, "current", true)
		if err != nil {
			return err
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := archive.Save(snapshots); err != nil {
		return fmt.Errorf("save log archive: %w", err)
	}
	return nil
}

func RunArchive(ctx context.Context) {
	_ = FlushArchives()
	ticker := time.NewTicker(ArchiveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = FlushArchives()
		}
	}
}

func (a *Archive) Healthy() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return !a.failed
}
func (c *Collector) ArchiveHealthy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.archive != nil && c.archive.Healthy()
}
