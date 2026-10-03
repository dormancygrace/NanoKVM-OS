// SPDX-FileCopyrightText: 2026 NanoKVM OS contributors
// SPDX-License-Identifier: MIT

package srtp

import (
	"encoding/binary"
	"testing"

	"github.com/pion/rtp"
)

// These benchmarks keep caller-owned destination storage and a parsed RTP
// header, matching the packet-level session path rather than measuring packet
// allocation or marshaling. Keys and packet contents are synthetic.
func BenchmarkCounterCMEncryptRTPParsed1200(b *testing.B) {
	SetAESCTRAccelerator(nil)
	context, err := buildTestContext(ProtectionProfileAes128CmHmacSha1_80)
	if err != nil {
		b.Fatal(err)
	}
	packet := make([]byte, 12+1200)
	header := rtp.Header{Version: 2, PayloadType: 96, SSRC: 0x12345678}
	if _, err = header.MarshalTo(packet); err != nil {
		b.Fatal(err)
	}
	for i := 12; i < len(packet); i++ {
		packet[i] = byte(i)
	}
	dst := make([]byte, len(packet)+10)
	// Prime the SSRC state and reusable buffers outside the measured loop.
	if dst, err = context.encryptRTP(dst, &header, 12, packet); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(1200)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		header.SequenceNumber++
		binary.BigEndian.PutUint16(packet[2:], header.SequenceNumber)
		if dst, err = context.encryptRTP(dst, &header, 12, packet); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkCounterEncryptRTCP(b *testing.B, profile ProtectionProfile) {
	b.Helper()
	SetAESCTRAccelerator(nil)
	context, err := buildTestContext(profile)
	if err != nil {
		b.Fatal(err)
	}
	// A synthetic, valid-size RTCP sender report with 20 bytes after its header.
	packet := make([]byte, 28)
	packet[0], packet[1] = 0x80, 200
	binary.BigEndian.PutUint16(packet[2:], 6)
	binary.BigEndian.PutUint32(packet[4:], 0x12345678)
	for i := 8; i < len(packet); i++ {
		packet[i] = byte(i)
	}
	dst := make([]byte, len(packet)+20)
	if dst, err = context.encryptRTCP(dst, packet); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(packet)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if dst, err = context.encryptRTCP(dst, packet); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCounterCMEncryptRTCP(b *testing.B) {
	benchmarkCounterEncryptRTCP(b, ProtectionProfileAes128CmHmacSha1_80)
}

func BenchmarkCounterGCMEncryptRTCP(b *testing.B) {
	benchmarkCounterEncryptRTCP(b, ProtectionProfileAeadAes128Gcm)
}
