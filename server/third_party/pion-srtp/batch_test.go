// SPDX-FileCopyrightText: 2026 NanoKVM Enhanced contributors
// SPDX-License-Identifier: MIT

package srtp

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec
	"sync"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/stretchr/testify/assert"
)

// softwareCTR stands in for the CryptoDMA packet accelerator; it declines
// every third payload so the software fallback runs inside a batch too.
type softwareCTR struct{ calls int }

func (b *softwareCTR) TryXORKeyStream(key, iv, dst, src []byte) bool {
	b.calls++
	if b.calls%3 == 0 {
		return false
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return false
	}
	cipher.NewCTR(block, iv).XORKeyStream(dst, src)
	return true
}

type softwareHMAC struct {
	key   []byte
	calls *int
}

func (h softwareHMAC) SumBatch(tags [][20]byte, msgs [][][]byte) {
	*h.calls++
	for i, parts := range msgs {
		mac := hmac.New(sha1.New, h.key)
		for _, p := range parts {
			mac.Write(p)
		}
		copy(tags[i][:], mac.Sum(nil))
	}
}

func batchTestKeys() (key, salt []byte) {
	key = bytes.Repeat([]byte{0x11}, 16)
	salt = bytes.Repeat([]byte{0x22}, 14)
	return key, salt
}

func frameBuffers(t *testing.T, count int, seq uint16) []*rtp.Packet {
	t.Helper()
	packets := make([]*rtp.Packet, count)
	for i := range packets {
		payload := bytes.Repeat([]byte{byte(i + 1)}, 1100+i*7)
		packets[i] = &rtp.Packet{Header: rtp.Header{
			Version: 2, PayloadType: 96, SSRC: 0x1234, Timestamp: 9000,
			SequenceNumber: seq + uint16(i), Marker: i == count-1,
		}, Payload: payload}
	}
	return packets
}

func TestEncryptRTPBatchMatchesPerPacket(t *testing.T) {
	defer SetAESCTRAccelerator(nil)
	defer SetHMACSHA1BatchFactory(nil)
	key, salt := batchTestKeys()
	for _, mode := range []struct {
		name      string
		aes, auth bool
	}{{"software", false, false}, {"aes", true, false}, {"auth", false, true}, {"both", true, true}} {
		t.Run(mode.name, func(t *testing.T) {
			var accel *softwareCTR
			SetAESCTRAccelerator(nil)
			if mode.aes {
				accel = &softwareCTR{}
				SetAESCTRAccelerator(accel)
			}
			authCalls := 0
			SetHMACSHA1BatchFactory(nil)
			if mode.auth {
				SetHMACSHA1BatchFactory(func(k []byte) HMACSHA1Batcher {
					return softwareHMAC{key: append([]byte(nil), k...), calls: &authCalls}
				})
			}
			plain, err := CreateContext(key, salt, ProtectionProfileAes128CmHmacSha1_80)
			assert.NoError(t, err)
			batched, err := CreateContext(key, salt, ProtectionProfileAes128CmHmacSha1_80)
			assert.NoError(t, err)
			// Two frames across the 16-bit sequence wrap: the ROC changes inside.
			for _, frame := range [][]*rtp.Packet{frameBuffers(t, 13, 65530), frameBuffers(t, 20, 7)} {
				var want [][]byte
				items := make([]rtpBatchItem, len(frame))
				for i, p := range frame {
					raw, err := p.Marshal()
					assert.NoError(t, err)
					enc, err := plain.EncryptRTP(nil, raw, nil)
					assert.NoError(t, err)
					want = append(want, enc)
					buf := make([]byte, len(raw)+20)
					copy(buf, raw)
					items[i] = rtpBatchItem{buf: buf, n: len(raw), headerLen: p.Header.MarshalSize(),
						ssrc: p.SSRC, seq: p.SequenceNumber}
				}
				got, err := batched.encryptRTPBatch(items)
				assert.NoError(t, err)
				assert.Equal(t, len(want), len(got))
				for i := range want {
					assert.Truef(t, bytes.Equal(want[i], got[i]), "packet %d differs", i)
				}
			}
			if mode.aes {
				// Both contexts use the accelerator: 33 payloads each.
				assert.Equal(t, 66, accel.calls, "every payload offered to the accelerator")
			}
			if mode.auth {
				assert.Equal(t, 2, authCalls)
			}
		})
	}
}

func TestSessionSRTPBatchesFramesAndKeepsOtherTraffic(t *testing.T) {
	defer SetHMACSHA1BatchFactory(nil)
	authCalls := 0
	SetHMACSHA1BatchFactory(func(k []byte) HMACSHA1Batcher {
		return softwareHMAC{key: append([]byte(nil), k...), calls: &authCalls}
	})
	aSession, bSession := buildSessionSRTPPair(t)
	var (
		readers sync.WaitGroup
		mu      sync.Mutex
		streams []*ReadStreamSRTP
	)
	defer func() {
		assert.NoError(t, aSession.Close())
		assert.NoError(t, bSession.Close())
		mu.Lock()
		for _, stream := range streams {
			assert.NoError(t, stream.Close())
		}
		mu.Unlock()
		readers.Wait()
	}()
	writer, err := aSession.OpenWriteStream()
	assert.NoError(t, err)

	type sent struct {
		ssrc    uint32
		seq     uint16
		payload []byte
	}
	var expect []sent
	write := func(p *rtp.Packet) {
		expect = append(expect, sent{p.SSRC, p.SequenceNumber, append([]byte(nil), p.Payload...)})
		_, err := writer.WriteRTP(&p.Header, p.Payload)
		assert.NoError(t, err)
	}
	received := make(chan sent, 64)
	readers.Add(1)
	go func() {
		defer readers.Done()
		for {
			stream, ssrc, err := bSession.AcceptStream()
			if err != nil {
				return
			}
			mu.Lock()
			streams = append(streams, stream)
			mu.Unlock()
			readers.Add(1)
			go func(stream *ReadStreamSRTP, ssrc uint32) {
				defer readers.Done()
				buf := make([]byte, 2048)
				for {
					n, header, err := stream.ReadRTP(buf)
					if err != nil {
						return
					}
					payload := append([]byte(nil), buf[header.MarshalSize():n]...)
					received <- sent{ssrc, header.SequenceNumber, payload}
				}
			}(stream, ssrc)
		}
	}()

	frame := frameBuffers(t, 13, 100)
	for i, p := range frame {
		write(p)
		if i == 5 {
			// Audio of another SSRC is written at once, between frame packets.
			write(&rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 111, SSRC: 0x99,
				SequenceNumber: 7}, Payload: bytes.Repeat([]byte{0xaa}, 160)})
		}
	}
	// A frame without a marker is flushed by the timer.
	tail := frameBuffers(t, 3, 113)
	tail[2].Marker = false
	for _, p := range tail {
		write(p)
	}

	got := map[[2]uint32][]byte{}
	deadline := time.After(2 * time.Second)
	for len(got) < len(expect) {
		select {
		case r := <-received:
			got[[2]uint32{r.ssrc, uint32(r.seq)}] = r.payload
		case <-deadline:
			t.Fatalf("received %d of %d packets", len(got), len(expect))
		}
	}
	for _, e := range expect {
		assert.Truef(t, bytes.Equal(got[[2]uint32{e.ssrc, uint32(e.seq)}], e.payload),
			"ssrc %#x seq %d payload differs", e.ssrc, e.seq)
	}
	assert.Equal(t, 2, authCalls, "the frame and the timed-out tail")
}
