// SPDX-License-Identifier: MIT

package dtls

import (
	"sync/atomic"
	"unsafe"

	"github.com/pion/dtls/v3/pkg/crypto/ciphersuite"
	"github.com/pion/dtls/v3/pkg/protocol/recordlayer"
)

// GCMBatchMax is the largest number of records WriteBatch seals in one call of the installed function.
const GCMBatchMax = 16

// GCMRecord is one record for the function of SetGCMRecordBatch. Its layout is that of struct
// hwcrypto_gcm_batch_record of the NanoKVM crypto driver, so the slice can go to the driver as it is: the
// counter block J0 (the nonce, then 00 00 00 01), the associated data, its length, the text's length, and where
// the text is. The text is sealed in place; the 16 bytes behind it are for the tag.
type GCMRecord struct {
	J0, AAD           [16]byte
	AADLength, Length uint32
	Data              unsafe.Pointer
}

// GCMRecordBatch seals records with one AES key; h is the hash key E_K(0). After an error it has written none
// of them.
type GCMRecordBatch func(key []byte, h *[16]byte, records []GCMRecord) error

var gcmRecordBatch atomic.Pointer[GCMRecordBatch] //nolint:gochecknoglobals

// SetGCMRecordBatch installs the function WriteBatch seals AES-GCM records with. Without one, WriteBatch is
// a Write for every payload.
func SetGCMRecordBatch(f GCMRecordBatch) {
	gcmRecordBatch.Store(&f)
}

// gcmBatchSuite is a cipher suite whose records can be sealed by a GCMRecordBatch.
type gcmBatchSuite interface {
	GCMBatch() (*ciphersuite.GCM, bool)
}

// WriteBatch writes every payload as an application-data record, in order, as Write does for each. With an
// AES-GCM suite and a function installed by SetGCMRecordBatch the records are built in one buffer and sealed
// together, GCMBatchMax at a time; otherwise, and for a single payload, it is Write. It returns at the first
// error; the payloads before it were written.
func (c *Conn) WriteBatch(payloads [][]byte) error {
	for len(payloads) > 0 {
		n := min(len(payloads), GCMBatchMax)
		done, err := c.writeGCMBatch(payloads[:n])
		if err != nil {
			return err
		}
		if !done {
			for _, payload := range payloads[:n] {
				if _, err = c.Write(payload); err != nil {
					return err
				}
			}
		}
		payloads = payloads[n:]
	}

	return nil
}

// writeGCMBatch seals and writes the payloads as one batch. It reports false, with nothing written and no
// sequence number taken, when that is not possible: fewer than two payloads, no function installed, another
// suite, a connection ID, a write deadline (Write follows it), or a batch that failed before.
func (c *Conn) writeGCMBatch(payloads [][]byte) (bool, error) {
	seal := gcmRecordBatch.Load()
	if seal == nil || len(payloads) < 2 || c.gcmBatchFailed.Load() {
		return false, nil
	}
	if _, set := c.writeDeadline.Deadline(); set {
		return false, nil
	}
	if c.isConnectionClosed() {
		return true, ErrConnClosed
	}
	if err := c.Handshake(); err != nil {
		return true, err
	}

	c.writeLock.Lock()
	defer c.writeLock.Unlock()

	c.lock.Lock()
	suite, ok := c.state.cipherSuite.(gcmBatchSuite)
	var gcm *ciphersuite.GCM
	if ok {
		gcm, ok = suite.GCMBatch()
	}
	if !ok || len(c.state.remoteConnectionID) > 0 {
		c.lock.Unlock()

		return false, nil
	}
	epoch := c.state.getLocalEpoch()
	for len(c.state.localSequenceNumber) <= int(epoch) {
		c.state.localSequenceNumber = append(c.state.localSequenceNumber, uint64(0))
	}
	count := uint64(len(payloads))
	first := atomic.AddUint64(&c.state.localSequenceNumber[epoch], count) - count
	if first+count-1 > recordlayer.MaxSequenceNumber {
		c.lock.Unlock()

		return true, errSequenceNumberOverflow
	}
	// The records one after another in the write buffer, which must not move once they point into it.
	size := 0
	for _, payload := range payloads {
		size += gcm.ApplicationDataSize(len(payload))
	}
	if cap(c.writeBuffer) < size {
		c.writeBuffer = make([]byte, 0, size)
	}
	buf := c.writeBuffer[:0]
	var records [GCMBatchMax]GCMRecord
	var ends [GCMBatchMax]int
	for i, payload := range payloads {
		var text int
		r := &records[i]
		buf, text = gcm.AppendPlainApplicationData(buf, epoch, first+uint64(i), payload, &r.J0, &r.AAD)
		r.AADLength, r.Length, r.Data = 13, uint32(len(payload)), unsafe.Pointer(&buf[text]) //nolint:gosec // G115
		ends[i] = len(buf)
	}
	key, h := gcm.BatchKey()
	rAddr := c.rAddr
	c.lock.Unlock()
	c.writeBuffer = buf

	if err := (*seal)(key, h, records[:len(payloads)]); err != nil {
		// Nothing was sealed and nothing was sent, so the sequence numbers are still unused on the wire:
		// the same records in software, and no more batches on this connection.
		c.gcmBatchFailed.Store(true)
		c.log.Warnf("AES-GCM record batch failed, records are sealed one by one from now on: %v", err)
		for i, payload := range payloads {
			sealed := gcm.SealApplicationData(c.gcmBatchSpare[:0], epoch, first+uint64(i), payload)
			c.gcmBatchSpare = sealed
			if err = c.writeDirect(sealed, rAddr); err != nil {
				return true, err
			}
		}

		return true, nil
	}
	start := 0
	for _, end := range ends[:len(payloads)] {
		if err := c.writeDirect(buf[start:end], rAddr); err != nil {
			return true, err
		}
		start = end
	}

	return true, nil
}
