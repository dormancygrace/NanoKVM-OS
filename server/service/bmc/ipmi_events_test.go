package bmc

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

// fakeIPMI serves GET /events like the add-on: one run (epoch) and its events.
type fakeIPMI struct {
	mu     sync.Mutex
	epoch  string
	events []ipmiEvent
}

func (f *fakeIPMI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	after, _ := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
	data := ipmiEvents{Epoch: f.epoch}
	for _, e := range f.events {
		if e.Seq > after {
			data.Events = append(data.Events, e)
		}
		data.Latest = e.Seq
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
}

func (f *fakeIPMI) set(epoch string, seqs ...uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.epoch, f.events = epoch, nil
	for _, seq := range seqs {
		f.events = append(f.events, ipmiEvent{Seq: seq, Kind: "login", User: "admin", Address: "10.0.0." + strconv.FormatUint(seq, 10)})
	}
}

// The backlog found when the server starts is not reported; new events are
// reported once; a restarted daemon is read from its first event.
func TestIPMIEventReader(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "ipmi.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeIPMI{}
	server := &http.Server{Handler: fake}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()

	r := newIPMIEventReader(socket)
	var got []uint64
	r.emit = func(e ipmiEvent) { got = append(got, e.Seq) }
	poll := func() {
		t.Helper()
		if err := r.poll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	fake.set("run1", 1, 2)
	poll()
	if len(got) != 0 {
		t.Fatalf("backlog reported: %v", got)
	}
	fake.set("run1", 1, 2, 3, 4)
	poll()
	poll()
	if want := []uint64{3, 4}; !equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	fake.set("run2", 1)
	poll()
	if want := []uint64{3, 4, 1}; !equal(got, want) {
		t.Fatalf("after restart got %v, want %v", got, want)
	}
}

func equal(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
