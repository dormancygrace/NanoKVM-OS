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

// A source file grows by appended lines up to archiveFileLimit and is then
// rewritten with the current snapshot. Reads still return at most MaxBytes
// and MaxLines, the newest ones, exactly as from a rewritten file.
const archiveFileLimit = 2 * MaxBytes

// The end of the last written snapshot. Finding it again in the next
// snapshot gives the lines to append.
const appendAnchorBytes = 4096

var sources = []string{"system", "kernel", "application"}
var bootSlots = []string{"current", "previous", "older"}
var validBootID = regexp.MustCompile(`^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$`)

type BootInfo struct {
	ID        string `json:"id"`
	StartedAt int64  `json:"startedAt"`
	SavedAt   int64  `json:"savedAt"`
}

// A window is committed with boot.json only after its newline-terminated
// source data is synced. Bytes after it may belong to an interrupted append.
// The digest distinguishes an atomic replacement whose metadata was not saved.
type archiveWindow struct {
	Offset int64  `json:"offset"`
	Length int    `json:"length"`
	Digest string `json:"sha256"`
}

type archiveSource struct {
	Lines       int            `json:"lines"`
	Truncated   bool           `json:"truncated"`
	CollectedAt int64          `json:"collectedAt"`
	Window      *archiveWindow `json:"window,omitempty"`
}

type bootRecord struct {
	BootID    string                   `json:"bootId"`
	StartedAt int64                    `json:"startedAt"`
	SavedAt   int64                    `json:"savedAt"`
	Sources   map[string]archiveSource `json:"sources"`
}

// Archive owns three fixed directories on the persistent Alpine filesystem.
// Only sanitized snapshots reach disk. Each source occupies at most two MiB.
type Archive struct {
	mu      sync.Mutex
	flushMu sync.Mutex
	root    *os.Root
	record  bootRecord
	digests map[string][32]byte
	// Retain the complete last snapshot to verify overlap, not just its
	// anchor. Slicing a 4 KiB anchor already kept this backing string alive.
	// Empty after a restart or failed save, forcing an atomic rewrite.
	snapshots map[string]string
	failed    bool
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
	a := &Archive{root: root, digests: map[string][32]byte{}, snapshots: map[string]string{}}
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

// writeSource brings current/<source>.log up to date with content. Usually
// that appends the lines after the previous snapshot's end, so a busy log
// does not rewrite up to a MiB of the SD card every minute. Anything else
// (first save of this process, no overlap, the file limit) rewrites it.
func (a *Archive) writeSource(source, content string) (int64, error) {
	name := "current/" + source + ".log"
	if previous := a.snapshots[source]; previous != "" {
		anchor := appendAnchor(previous)
		// Repeated anchors are ambiguous. A unique match still needs the
		// complete overlap and both line boundaries checked: text before or
		// within the matched line may have changed after rotation/truncation.
		at := strings.LastIndex(content, anchor)
		if at >= 0 && strings.Index(content, anchor) == at {
			end := at + len(anchor)
			overlap := content[:end]
			begins := len(previous) - len(overlap)
			if strings.HasSuffix(previous, overlap) &&
				(begins == 0 || previous[begins-1] == '\n') &&
				(end == len(content) || content[end] == '\n') {
				delta := ""
				if end < len(content) {
					// The existing file already ends with the separator.
					delta = content[end+1:] + "\n"
				}
				size, appended, err := a.appendFile(name, delta)
				if err != nil {
					delete(a.snapshots, source)
					return 0, err
				}
				if appended {
					a.snapshots[source] = content
					return size - int64(len(content)+1), nil
				}
			}
		}
	}
	delete(a.snapshots, source)
	if err := a.atomicWrite(name, []byte(content+"\n")); err != nil {
		return 0, err
	}
	a.snapshots[source] = content
	return 0, nil
}

func appendAnchor(content string) string {
	return content[max(0, len(content)-appendAnchorBytes):]
}

// appendFile returns the resulting size. It writes nothing and returns false
// when the file would exceed the bound. Even an empty delta validates the file.
func (a *Archive) appendFile(name, text string) (int64, bool, error) {
	before, err := a.root.Lstat(name)
	if err != nil {
		return 0, false, err
	}
	if !before.Mode().IsRegular() {
		return 0, false, errors.New("invalid archive file")
	}
	file, err := a.root.OpenFile(name, os.O_WRONLY|os.O_APPEND|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return 0, false, err
	}
	info, err := file.Stat()
	if err == nil && (!info.Mode().IsRegular() || !os.SameFile(before, info)) {
		err = errors.New("archive file changed")
	}
	if err != nil {
		file.Close()
		return 0, false, err
	}
	size := info.Size() + int64(len(text))
	if size > archiveFileLimit {
		return 0, false, file.Close()
	}
	if text != "" {
		_, err = file.WriteString(text)
		if err == nil {
			err = file.Sync()
		}
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return size, err == nil, err
}

// committedSource selects the latest committed snapshot, excluding both older
// accumulated lines and an uncommitted suffix. An atomic rewrite may precede
// its metadata update; if the old window no longer matches, recover complete
// lines from that file and mark the result truncated. Legacy files have no
// window and their last saved entry was not newline-terminated.
func committedSource(data []byte, window *archiveWindow) ([]byte, bool) {
	if window == nil {
		return append(data, '\n'), false
	}
	if window.Offset >= 0 && window.Offset <= int64(len(data)) &&
		window.Length > 0 && window.Length <= MaxBytes+1 &&
		int64(window.Length) <= int64(len(data))-window.Offset {
		candidate := data[window.Offset : window.Offset+int64(window.Length)]
		if candidate[len(candidate)-1] == '\n' &&
			fmt.Sprintf("%x", sha256.Sum256(candidate[:len(candidate)-1])) == window.Digest {
			return candidate, false
		}
	}
	// sanitizeTail will discard an incomplete final entry; never synthesize
	// a newline for a file written in the new format.
	return data, true
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
	data, err := readArchiveFile(a.root, slot+"/"+source+".log", archiveFileLimit)
	if err != nil {
		return Snapshot{}, err
	}
	// Apply the same output policy again to defend against modified archives.
	data, recovered := committedSource(data, meta.Window)
	content, lines, clipped := sanitizeTail(data, false)
	state := "ready"
	if content == "" {
		state = "empty"
	}
	return Snapshot{Source: source, Boot: slot, State: state, Content: content, Lines: lines, Truncated: meta.Truncated || clipped || recovered, CollectedAt: meta.CollectedAt}, nil
}

func (a *Archive) Save(snapshots []Snapshot) (saveErr error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	defer func() {
		a.failed = saveErr != nil
		if saveErr != nil {
			// A source may have changed before another write or boot.json
			// failed. Never skip/reuse that uncommitted state on the retry.
			clear(a.digests)
			clear(a.snapshots)
		}
	}()
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
		offset, err := a.writeSource(snapshot.Source, snapshot.Content)
		if err != nil {
			return err
		}
		digests[snapshot.Source] = digest
		record.Sources[snapshot.Source] = archiveSource{
			Lines: snapshot.Lines, Truncated: snapshot.Truncated, CollectedAt: snapshot.CollectedAt,
			Window: &archiveWindow{Offset: offset, Length: len(snapshot.Content) + 1, Digest: fmt.Sprintf("%x", digest)},
		}
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
