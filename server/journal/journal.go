// Package journal keeps the device's event journal: what happened to the
// host (power, picture, a crash on its screen), who logged in from where,
// and who pressed a button or changed the virtual drive's medium. It is the
// record a BMC keeps as its event log.
//
// The journal is a file of JSON lines on the card, bounded in entries and in
// what a minute may add: events are rare, but an outsider can cause some at
// will.
package journal

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/internal/atomicfile"
)

const (
	// Max is how many entries are kept; the file is rewritten from the
	// newest Max when it holds twice as many.
	Max = 500
	// maxPerMinute bounds what a minute adds. What is left out is counted
	// and told by an entry of its own.
	maxPerMinute = 30
	maxDetails   = 2000
)

// Entry is one event. Message is a key of the notifications' messages, with
// Vars filled in when it is shown.
type Entry struct {
	Seq      uint64            `json:"seq"`
	Time     time.Time         `json:"time"`
	Kind     string            `json:"kind"`
	Severity string            `json:"severity"`
	Message  string            `json:"message"`
	Vars     map[string]string `json:"vars,omitempty"`
	Details  string            `json:"details,omitempty"`
}

type Journal struct {
	Path string
	Now  func() time.Time

	mu      sync.Mutex
	loaded  bool
	entries []Entry
	lines   int // lines in the file
	seq     uint64
	recent  []time.Time // of the entries added in the last minute
	dropped int
}

// Default is the device's journal.
var Default = &Journal{Path: "/var/lib/nanokvm/events.jsonl", Now: time.Now}

// load reads the file once. A line that is not an entry (a write cut by a
// power loss) is skipped.
func (j *Journal) load() {
	if j.loaded {
		return
	}
	j.loaded = true
	data, err := os.ReadFile(j.Path)
	if err != nil {
		return
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(nil, 1<<20)
	for scanner.Scan() {
		j.lines++
		var e Entry
		if json.Unmarshal(scanner.Bytes(), &e) == nil && e.Message != "" {
			j.entries = append(j.entries, e)
			j.seq = max(j.seq, e.Seq)
		}
	}
	j.trim()
}

func (j *Journal) trim() {
	if len(j.entries) > Max {
		j.entries = append([]Entry(nil), j.entries[len(j.entries)-Max:]...)
	}
}

// Add records an event; Seq is the journal's, Time is now unless set.
func (j *Journal) Add(e Entry) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.load()
	now := j.Now()
	for len(j.recent) > 0 && now.Sub(j.recent[0]) >= time.Minute {
		j.recent = j.recent[1:]
	}
	if len(j.recent) >= maxPerMinute {
		j.dropped++
		return
	}
	if j.dropped > 0 {
		j.append(Entry{Time: now, Kind: "journal", Severity: "warning", Message: "journal.dropped",
			Vars: map[string]string{"count": strconv.Itoa(j.dropped)}})
		j.dropped = 0
	}
	if e.Time.IsZero() {
		e.Time = now
	}
	if len(e.Details) > maxDetails {
		e.Details = e.Details[:maxDetails]
	}
	j.recent = append(j.recent, now)
	j.append(e)
}

func (j *Journal) append(e Entry) {
	j.seq++
	e.Seq = j.seq
	j.entries = append(j.entries, e)
	j.trim()
	if err := j.write(e); err != nil {
		log.Errorf("event journal: %v", err)
	}
}

func (j *Journal) write(e Entry) error {
	if j.lines >= 2*Max {
		var all bytes.Buffer
		for _, kept := range j.entries {
			line, _ := json.Marshal(kept)
			all.Write(append(line, '\n'))
		}
		j.lines = len(j.entries)
		return atomicfile.Write(j.Path, all.Bytes(), 0o600)
	}
	if err := os.MkdirAll(filepath.Dir(j.Path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(j.Path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	line, _ := json.Marshal(e)
	if _, err = file.Write(append(line, '\n')); err != nil {
		return err
	}
	j.lines++
	return file.Sync()
}

// Entries returns the newest entries, newest first, at most limit.
func (j *Journal) Entries(limit int) []Entry {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.load()
	if limit <= 0 || limit > len(j.entries) {
		limit = len(j.entries)
	}
	out := make([]Entry, 0, limit)
	for i := len(j.entries) - 1; i >= len(j.entries)-limit; i-- {
		out = append(out, j.entries[i])
	}
	return out
}

type actorKey struct{}

// WithActor names who a request acts for ("admin (web)", "Redfish"), for
// the entries of what the request does.
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

// Actor is the actor of ctx, the NanoKVM itself when none is named.
func Actor(ctx context.Context) string {
	if actor, ok := ctx.Value(actorKey{}).(string); ok && actor != "" {
		return actor
	}
	return "NanoKVM"
}

// Action records something done to the host.
func Action(ctx context.Context, message string, vars map[string]string) {
	if vars == nil {
		vars = map[string]string{}
	}
	vars["actor"] = Actor(ctx)
	Default.Add(Entry{Kind: "action", Severity: "info", Message: message, Vars: vars})
}
