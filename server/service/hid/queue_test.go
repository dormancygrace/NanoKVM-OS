package hid

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

// Ownership can be revoked after a key-down was admitted. The release callback
// must still run even when the normal admission callback refuses all writes.
func TestRevokedExternalKeyboardStillReleasesHeldKey(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	h := &Hid{g0: writer}
	queue := make(chan QueuedReport)
	completed := make(chan bool, 1)
	revoked := false
	queueDone := make(chan struct{})
	go func() { h.keyboardReports(queue, "unused-keyboard"); close(queueDone) }()
	queue <- QueuedReport{Data: []byte{0, 0, 4, 0, 0, 0, 0, 0},
		Execute: func(write func() error) error {
			if revoked {
				return errors.New("revoked")
			}
			return write()
		},
		Cleanup:  func(write func() error) error { return write() },
		Complete: func(ok bool) { completed <- ok },
	}
	if !<-completed {
		t.Fatal("key-down failed")
	}
	revoked = true
	close(queue)
	select {
	case <-queueDone:
	case <-time.After(time.Second):
		t.Fatal("release blocked")
	}
	data := make([]byte, 16)
	reader.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := io.ReadFull(reader, data); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data[8:], keyboardReleaseReport()) {
		t.Fatal("held key did not release", data)
	}
}
