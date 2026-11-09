// SPDX-License-Identifier: AGPL-3.0-only
package rustdesk

import (
	"bytes"

	"encoding/binary"

	"io"

	"strings"

	"testing"
)

type rtcShortWriter struct{ bytes.Buffer }

func (w *rtcShortWriter) Write(b []byte) (int, error) { return w.Buffer.Write(b[:min(len(b), 3)]) }
func TestRustDeskRTCFraming(t *testing.T) {
	for _, n := range []int{0, 1, 63, 64, 16383, 16384, 60000, 130000, 1 << 22} {
		data := bytes.Repeat([]byte{0xa5}, n)
		var w rtcShortWriter
		if err := writeRustDeskPayload(&w, data); err != nil {
			t.Fatal(err)
		}
		got, err := readRustDeskPayload(&w)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("roundtrip %d: %v", n, err)
		}
	}
	var w rtcShortWriter
	if err := writeRTCJSON(&w, rtcEvent{Event: "ready"}); err != nil {
		t.Fatal(err)
	}
	var event rtcEvent
	if err := readRTCJSON(&w, &event); err != nil || event.Event != "ready" {
		t.Fatal(err, event)
	}
	if err := writeRustDeskPayload(io.Discard, make([]byte, rtcMaxMessage+1)); err == nil {
		t.Fatal("oversize accepted")
	}
	var header [4]byte
	binary.LittleEndian.PutUint32(header[:], uint32(rtcMaxMessage+1)<<2|3)
	if _, err := readRustDeskPayload(bytes.NewReader(header[:])); err == nil {
		t.Fatal("oversize read")
	}
	var fragments []byte
	if _, done, err := appendRTCFragment(&fragments, append([]byte{1}, bytes.Repeat([]byte{1}, 60000)...)); err != nil || done {
		t.Fatal(err)
	}
	payload, done, err := appendRTCFragment(&fragments, []byte{0, 2})
	if err != nil || !done || len(payload) != 60001 || fragments != nil {
		t.Fatal("reassembly")
	}
	for _, bad := range [][]byte{nil, {1}, {2}, make([]byte, rtcMaxFragment+2)} {
		if _, _, err := appendRTCFragment(&fragments, bad); err == nil {
			t.Fatal("bad fragment")
		}
	}
	fragments = make([]byte, rtcMaxInbound)
	if _, _, err := appendRTCFragment(&fragments, []byte{0, 1}); err == nil {
		t.Fatal("unbounded assembly")
	}
	fp := strings.Repeat("AB:", 31) + "AB"
	if got, err := rtcFingerprint("a=fingerprint:sha-256 " + fp + "\r\n"); err != nil || got != "sha-256 "+fp {
		t.Fatal(got, err)
	}
	if _, err := rtcFingerprint("a=fingerprint:sha-256 " + fp + "\na=fingerprint:sha-256 " + strings.ReplaceAll(fp, "AB", "CD")); err == nil {
		t.Fatal("ambiguous certificate")
	}
}

// Configure before publishing the listener or starting accept.

// The shared DTLS policy: NanoKVM answers as the DTLS server.

// Ordinary input bursts must survive the bounded queue.

// No PeerConnection is constructed once all four slots, including teardown, are held.

func readRustDeskPayload(r io.Reader) ([]byte, error) {
	var first [1]byte
	if _, err := io.ReadFull(r, first[:]); err != nil {
		return nil, err
	}
	return readRustDeskPayloadAfterHead(r, first[0])
}
