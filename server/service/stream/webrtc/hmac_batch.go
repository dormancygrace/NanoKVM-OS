package webrtc

import (
	"NanoKVM-Server/internal/sha1mb"
	"sync"

	"github.com/pion/srtp/v3"
)

// sha1Batch authenticates the packets of a video frame together: four
// messages per XTheadVector step on the C906, crypto/sha1 elsewhere (see
// internal/sha1mb). Each SRTP cipher owns one; the SRTP context lock
// serializes its calls, so msgs is reused across frames.
type sha1Batch struct {
	key  *sha1mb.Key
	msgs []sha1mb.Message
}

func (b *sha1Batch) SumBatch(tags [][20]byte, msgs [][][]byte) {
	b.msgs = b.msgs[:0]
	for _, parts := range msgs {
		var m sha1mb.Message
		copy(m[:], parts)
		b.msgs = append(b.msgs, m)
	}
	b.key.SumBatch(tags, b.msgs)
	clear(b.msgs)
}

var hmacBatchOnce sync.Once

func initializeHMACBatch() {
	hmacBatchOnce.Do(func() {
		srtp.SetHMACSHA1BatchFactory(func(key []byte) srtp.HMACSHA1Batcher {
			return &sha1Batch{key: sha1mb.NewKey(key)}
		})
	})
}
