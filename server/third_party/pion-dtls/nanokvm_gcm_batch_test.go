// SPDX-License-Identifier: MIT

package dtls

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"testing"
	"time"
	"unsafe"

	"github.com/pion/transport/v5/dpipe"
)

// WriteBatch hands the installed function records the peer opens: the function here seals them with crypto/cipher
// from exactly what it is given (key, hash key, J0, associated data, text in place, tag behind it). More payloads
// than one batch holds, a Write between batches, and a function that fails: the same records go out sealed one
// by one, and the function is not asked again.
func TestWriteBatchSealsRecordsThePeerOpens(t *testing.T) {
	ca, cb := dpipe.Pipe()
	client, server, err := pipeConn(ca, cb)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close(); _ = server.Close() }()

	calls, sealed, fail := 0, 0, false
	SetGCMRecordBatch(func(key []byte, h *[16]byte, records []GCMRecord) error {
		calls++
		if fail {
			return errors.New("no engine")
		}
		block, _ := aes.NewCipher(key)
		var hashKey [16]byte
		block.Encrypt(hashKey[:], hashKey[:])
		if hashKey != *h || len(records) > GCMBatchMax {
			t.Errorf("hash key or batch size: %d records", len(records))
		}
		gcm, _ := cipher.NewGCM(block)
		for i := range records {
			r := &records[i]
			text := unsafe.Slice((*byte)(r.Data), int(r.Length)+16)
			if r.J0[12] != 0 || r.J0[13] != 0 || r.J0[14] != 0 || r.J0[15] != 1 || r.AADLength != 13 {
				t.Errorf("record %d: J0 %x, %d bytes of associated data", i, r.J0, r.AADLength)
			}
			gcm.Seal(text[:0], r.J0[:12], text[:r.Length], r.AAD[:r.AADLength])
			sealed++
		}

		return nil
	})
	defer gcmRecordBatch.Store(nil)

	payload := func(i int) []byte { return bytes.Repeat([]byte{byte(i + 1)}, 1+i*61) }
	expect := func(from, to int) {
		t.Helper()
		buf := make([]byte, 2048)
		for i := from; i < to; i++ {
			_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
			n, err := client.Read(buf)
			if err != nil || !bytes.Equal(buf[:n], payload(i)) {
				t.Fatalf("payload %d: %d bytes, %v", i, n, err)
			}
		}
	}
	var batch [][]byte
	for i := range 20 {
		batch = append(batch, payload(i))
	}
	if err = server.WriteBatch(batch); err != nil {
		t.Fatal(err)
	}
	expect(0, 20)
	if calls != 2 || sealed != 20 {
		t.Fatalf("the function sealed %d records in %d calls: the suite is not AES-GCM, or the batches are not 16 and 4", sealed, calls)
	}
	if _, err = server.Write(payload(20)); err != nil {
		t.Fatal(err)
	}
	expect(20, 21)

	// A payload larger than the function takes: the records are written one by one, the function is not asked.
	big := bytes.Repeat([]byte{0xb1}, gcmBatchPayload+1)
	if err = server.WriteBatch([][]byte{big, payload(1)}); err != nil {
		t.Fatal(err)
	}
	bigBuf := make([]byte, 2*gcmBatchPayload)
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, err := client.Read(bigBuf); err != nil || !bytes.Equal(bigBuf[:n], big) {
		t.Fatalf("large payload: %d bytes, %v", n, err)
	}
	expect(1, 2)
	if calls != 2 {
		t.Fatalf("%d calls: a batch with a payload over %d bytes went to the function", calls, gcmBatchPayload)
	}

	fail = true
	if err = server.WriteBatch([][]byte{payload(21), payload(22), payload(23)}); err != nil {
		t.Fatal(err)
	}
	expect(21, 24)
	if err = server.WriteBatch([][]byte{payload(24), payload(25)}); err != nil {
		t.Fatal(err)
	}
	expect(24, 26)
	if calls != 3 {
		t.Fatalf("%d calls: a failed function is asked once and not again", calls)
	}
}
