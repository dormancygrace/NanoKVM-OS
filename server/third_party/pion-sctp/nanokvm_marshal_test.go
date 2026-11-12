// SPDX-License-Identifier: MIT

package sctp

import (
	"bytes"
	"testing"
)

// marshalInto must make the bytes upstream's marshal made: the packet header, every chunk's own marshal, each
// padded to four bytes, and the checksum. DATA and I-DATA chunks take the path that does not copy.
func TestMarshalIntoMatchesChunkMarshal(t *testing.T) {
	data := func(n int, first, last, iData bool) *chunkPayloadData {
		payload := make([]byte, n)
		for i := range payload {
			payload[i] = byte(3*i + n)
		}

		return &chunkPayloadData{
			tsn: 0x01020304, streamIdentifier: 5, streamSequenceNumber: 6, messageIdentifier: 7,
			fragmentSequenceNumber: 8, payloadType: PayloadTypeWebRTCBinary, userData: payload,
			beginningFragment: first, endingFragment: last, immediateSack: n%2 == 1, iData: iData,
		}
	}
	sack := &chunkSelectiveAck{cumulativeTSNAck: 9, advertisedReceiverWindowCredit: 10}
	for _, chunks := range [][]chunk{
		{data(1, true, true, false)},
		{data(1175, true, false, false)},
		{sack, data(13, false, true, false), data(14, true, true, false)},
		{data(1171, true, false, true)},
		{data(2, false, false, true), sack, data(3, false, true, true)},
	} {
		pkt := &packet{sourcePort: 5000, destinationPort: 5001, verificationTag: 0x0a0b0c0d, chunks: chunks}
		want := []byte{0x13, 0x88, 0x13, 0x89, 0x0a, 0x0b, 0x0c, 0x0d, 0, 0, 0, 0}
		for _, c := range chunks {
			raw, err := c.marshal()
			if err != nil {
				t.Fatal(err)
			}
			want = append(want, raw...)
			want = append(want, make([]byte, getPadding(len(want)))...)
		}
		sum := generatePacketChecksum(want)
		want[8], want[9], want[10], want[11] = byte(sum), byte(sum>>8), byte(sum>>16), byte(sum>>24)

		for _, buf := range [][]byte{nil, make([]byte, 7, 16), make([]byte, 0, 2048)} {
			got, err := pkt.marshalInto(buf, true)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("%d chunks into a buffer of %d: differs from the chunks' marshal (%v)", len(chunks), cap(buf), err)
			}
		}
	}
}
