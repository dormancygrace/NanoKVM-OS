// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

package dtls

import (
	"bytes"
	"io"
	"testing"

	"github.com/pion/dtls/v3/internal/closer"
	"github.com/pion/transport/v5/deadline"
)

func TestReadDrainsAuthenticatedRecordBeforeEOF(t *testing.T) {
	// Model a completed receive loop with its last record still buffered.
	// Both select cases are ready; EOF must never win over the record.
	for i := 0; i < 100; i++ {
		c := &Conn{
			closed:       closer.NewCloser(),
			readDeadline: deadline.New(),
			decrypted:    make(chan any, 1),
		}
		c.handshakeCompletedSuccessfully.Store(true)
		want := []byte("last authenticated application record")
		c.decrypted <- want
		close(c.decrypted)
		c.closed.Close()

		buf := make([]byte, 128)
		n, err := c.Read(buf)
		if err != nil || !bytes.Equal(buf[:n], want) {
			t.Fatalf("iteration %d: buffered record lost: n=%d err=%v", i, n, err)
		}
		if n, err := c.Read(buf); n != 0 || err != io.EOF {
			t.Fatalf("after buffered record: n=%d err=%v, want EOF", n, err)
		}
	}
}
