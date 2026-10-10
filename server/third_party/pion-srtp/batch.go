// SPDX-FileCopyrightText: 2026 NanoKVM Enhanced contributors
// SPDX-License-Identifier: MIT

package srtp

import (
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/rtp"
)

// HMACSHA1Batcher computes HMAC-SHA1 under one key for several messages, each
// given as parts that are authenticated as if concatenated.
type HMACSHA1Batcher interface {
	SumBatch(tags [][20]byte, msgs [][][]byte)
}

var hmacBatchRegistry struct {
	sync.RWMutex
	factory func(key []byte) HMACSHA1Batcher
}

// SetHMACSHA1BatchFactory selects the batch authenticator for subsequently
// created AES-CM contexts; nil keeps crypto/hmac for each packet.
func SetHMACSHA1BatchFactory(factory func(key []byte) HMACSHA1Batcher) {
	hmacBatchRegistry.Lock()
	hmacBatchRegistry.factory = factory
	hmacBatchRegistry.Unlock()
}

func newHMACSHA1Batcher(key []byte) HMACSHA1Batcher {
	hmacBatchRegistry.RLock()
	factory := hmacBatchRegistry.factory
	hmacBatchRegistry.RUnlock()
	if factory == nil {
		return nil
	}
	return factory(key)
}

// rtpBatchingWanted reports whether frames should be gathered at all: only a
// batch authenticator makes it cheaper than writing each packet at once.
func rtpBatchingWanted() bool {
	hmacBatchRegistry.RLock()
	defer hmacBatchRegistry.RUnlock()
	return hmacBatchRegistry.factory != nil
}

// rtpBatchItem is one marshalled RTP packet in buf[:n]; buf has room for the
// tag and becomes the protected packet in place.
type rtpBatchItem struct {
	pbuf      *[]byte
	buf       []byte
	n         int
	headerLen int
	ssrc      uint32
	seq       uint16
	roc       uint32
}

// encryptRTPBatch protects packets in order, in place. Profiles and options
// the batch path does not cover take the per-packet path for the whole batch.
func (c *Context) encryptRTPBatch(items []rtpBatchItem) ([][]byte, error) {
	out := make([][]byte, len(items))
	aesCM, ok := c.cipher.(*srtpCipherAesCmHmacSha1)
	if !ok || c.cryptexMode != CryptexModeDisabled || c.rccMode != RCCModeNone || len(aesCM.mki) > 0 {
		for i := range items {
			it := &items[i]
			var header rtp.Header
			headerLen, err := header.Unmarshal(it.buf[:it.n])
			if err != nil {
				return out[:i], err
			}
			enc, err := c.encryptRTP(it.buf, &header, headerLen, it.buf[:it.n])
			if err != nil {
				return out[:i], err
			}
			out[i] = enc
		}
		return out, nil
	}
	for i := range items {
		it := &items[i]
		ssrcState, _ := c.getSRTPSSRCState(it.ssrc, true)
		roc, diff, ovf := ssrcState.nextRolloverCount(it.seq)
		if ovf {
			return out[:i], errExceededMaxPackets
		}
		ssrcState.updateRolloverCount(it.seq, diff, false, 0)
		it.roc = roc
	}
	return aesCM.encryptRTPBatch(items, out)
}

func (s *srtpCipherAesCmHmacSha1) encryptRTPBatch(items []rtpBatchItem, out [][]byte) ([][]byte, error) {
	tagLen, err := s.AuthTagRTPLen()
	if err != nil {
		return nil, err
	}
	n := len(items)
	if s.srtpEncrypted {
		// Per packet, through the optional AESCTRAccelerator as encryptRTP does.
		for i := range items {
			it := &items[i]
			iv := generateCounter(it.seq, it.roc, it.ssrc, s.srtpSessionSalt)
			payload := it.buf[it.headerLen:it.n]
			if err := xorBytesCTR(s.srtpBlock, iv[:], payload, payload); err != nil {
				return nil, err
			}
		}
	}
	if s.srtpAuthBatch != nil {
		tags := make([][20]byte, n)
		rocs := make([][4]byte, n)
		msgs := make([][][]byte, n)
		for i := range items {
			it := &items[i]
			binary.BigEndian.PutUint32(rocs[i][:], it.roc)
			msgs[i] = [][]byte{it.buf[:it.n], rocs[i][:]}
		}
		s.srtpAuthBatch.SumBatch(tags, msgs)
		for i := range items {
			it := &items[i]
			copy(it.buf[it.n:], tags[i][:tagLen])
			out[i] = it.buf[:it.n+tagLen]
		}
		return out, nil
	}
	for i := range items {
		it := &items[i]
		tag, err := s.generateSrtpAuthTag(it.buf[:it.n], it.roc, false)
		if err != nil {
			return nil, err
		}
		copy(it.buf[it.n:], tag)
		out[i] = it.buf[:it.n+tagLen]
	}
	return out, nil
}

// Frames are gathered while packets of one SSRC arrive in sequence and are
// protected together at the marker bit (the last packet of a video frame),
// at batchMaxPackets, or after batchFlushDelay should a frame end without
// one. Packets of other SSRCs (audio) and out-of-sequence packets (NACK
// retransmissions) are written at once; the latter flush the pending run
// first so a stream's packets keep their order.
const (
	batchMaxPackets = 32
	batchFlushDelay = 2 * time.Millisecond
	batchMinPayload = 256
)

// Lock order and shutdown. mu guards items and timer and, to keep the packets
// of a stream in order, is held across the encryption and the transport writes
// of a flush, so a write blocked by transport backpressure keeps mu. Shutdown
// must therefore not wait for mu before the transport is closed: Close sets
// closed (atomic, never needs mu), closes the transport so a blocked write
// returns and releases mu, and only then takes mu to drop what is queued.
// Under mu the order is mu, then session.localContextMutex. closed is read
// under mu by the enqueue path and by every flush, again before each
// transport write, so after Close no enqueue, timer callback or flush starts
// a write or arms the timer.
type rtpBatch struct {
	closed atomic.Bool
	mu     sync.Mutex
	items  []rtpBatchItem
	timer  *time.Timer
}

// writeRTPBatched returns handled=false when the packet should be written at
// once by the caller.
func (s *SessionSRTP) writeRTPBatched(header *rtp.Header, payload []byte) (n int, handled bool, err error) {
	b := &s.batch
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed.Load() {
		return 0, true, net.ErrClosed
	}
	if len(b.items) > 0 {
		last := b.items[len(b.items)-1]
		if header.SSRC != last.ssrc {
			return 0, false, nil
		}
		if header.SequenceNumber != last.seq+1 {
			if err := s.flushBatchLocked(); err != nil {
				return 0, true, err
			}
			return 0, false, nil
		}
	} else if header.Marker || len(payload) < batchMinPayload {
		return 0, false, nil
	}
	pbuf, ok := bufferpool.Get().(*[]byte)
	if !ok {
		return 0, true, errStartedChannelUsedIncorrectly
	}
	buf := *pbuf
	headerLen, marshalSize := rtp.HeaderAndPacketMarshalSize(header, payload) // nolint:staticcheck
	if len(buf) < marshalSize+20 {
		buf = make([]byte, marshalSize+20)
	}
	if _, err := rtp.MarshalPacketTo(buf, header, payload); err != nil { // nolint:staticcheck
		bufferpool.Put(pbuf)
		return 0, true, err
	}
	b.items = append(b.items, rtpBatchItem{
		pbuf: pbuf, buf: buf, n: marshalSize, headerLen: headerLen,
		ssrc: header.SSRC, seq: header.SequenceNumber,
	})
	if header.Marker || len(b.items) >= batchMaxPackets {
		return marshalSize, true, s.flushBatchLocked()
	}
	if b.timer == nil {
		b.timer = time.AfterFunc(batchFlushDelay, s.flushBatchTimer)
	} else if len(b.items) == 1 {
		b.timer.Reset(batchFlushDelay)
	}
	return marshalSize, true, nil
}

func (s *SessionSRTP) flushBatchTimer() {
	s.batch.mu.Lock()
	defer s.batch.mu.Unlock()
	if err := s.flushBatchLocked(); err != nil && !errors.Is(err, net.ErrClosed) {
		s.session.log.Debugf("srtp: delayed frame write failed: %v", err)
	}
}

func (s *SessionSRTP) flushBatchLocked() error {
	b := &s.batch
	if len(b.items) == 0 {
		return nil
	}
	if b.timer != nil {
		b.timer.Stop()
	}
	items := b.items
	b.items = b.items[:0]
	defer func() {
		for i := range items {
			bufferpool.Put(items[i].pbuf)
			items[i] = rtpBatchItem{}
		}
	}()
	if b.closed.Load() {
		return net.ErrClosed
	}
	s.session.localContextMutex.Lock()
	packets, err := s.localContext.encryptRTPBatch(items)
	s.session.localContextMutex.Unlock()
	for _, packet := range packets {
		if b.closed.Load() {
			return net.ErrClosed
		}
		if _, werr := s.session.nextConn.Write(packet); werr != nil && err == nil {
			err = werr
		}
	}
	return err
}

// closeBatch refuses further batched writes. It does not take mu, which a
// flush blocked in the transport may hold.
func (s *SessionSRTP) closeBatch() {
	s.batch.closed.Store(true)
}

// discardBatch drops a pending frame when the session closes. It takes mu, so
// it must run after the transport is closed (see the lock order above).
func (s *SessionSRTP) discardBatch() {
	b := &s.batch
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.timer != nil {
		b.timer.Stop()
	}
	for i := range b.items {
		bufferpool.Put(b.items[i].pbuf)
	}
	b.items = nil
}
