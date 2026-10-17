package handshake

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/pion/dtls/v3/internal/ciphersuite/types"
	"testing"
)

func TestNanoKVMLargePSKIdentityHint(t *testing.T) {
	for _, n := range []int{65533, 65534, 65535} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			wire := make([]byte, 2+n)
			binary.BigEndian.PutUint16(wire, uint16(n))
			for i := 2; i < len(wire); i++ {
				wire[i] = 0xa5
			}
			m := &MessageServerKeyExchange{KeyExchangeAlgorithm: types.KeyExchangeAlgorithmPsk}
			if err := m.Unmarshal(wire); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(m.IdentityHint, wire[2:]) {
				t.Fatal("identity hint truncated")
			}
			if err := m.Unmarshal(wire[:len(wire)-1]); err == nil {
				t.Fatal("accepted truncated hint")
			}
		})
	}
}

func TestNanoKVMCertificateEntryBoundaries(t *testing.T) {
	for _, wire := range [][]byte{{0, 0, 3, 0, 0, 0}, {0, 0, 1, 0}, {0, 0, 2, 0, 0}, {0, 0, 4, 0, 0, 2, 1}} {
		if err := (&MessageCertificate{}).Unmarshal(wire); err == nil {
			t.Fatalf("accepted malformed certificate list %x", wire)
		}
	}
	m := &MessageCertificate{}
	if err := m.Unmarshal([]byte{0, 0, 0}); err != nil {
		t.Fatal("empty certificate list is valid:", err)
	}
	if _, err := (&MessageCertificate{Certificate: [][]byte{{}}}).Marshal(); err == nil {
		t.Fatal("encoded empty certificate entry")
	}
}
