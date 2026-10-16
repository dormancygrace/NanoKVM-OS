package recordlayer

import (
	"bytes"
	"github.com/pion/dtls/v3/pkg/protocol"
	"testing"
)

func TestNanoKVMInnerPlaintextBoundaries(t *testing.T) {
	for _, raw := range [][]byte{nil, {}, {0}, {0, 0}, make([]byte, 256)} {
		var got InnerPlaintext
		if err := got.Unmarshal(raw); err == nil {
			t.Fatalf("accepted missing content type: %x", raw)
		}
	}
	for _, payload := range [][]byte{nil, {0}, {1, 0, 2}} {
		for _, zeros := range []uint{0, 1, 256} {
			want := InnerPlaintext{Content: payload, RealType: protocol.ContentTypeApplicationData, Zeros: zeros}
			raw, err := want.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			var got InnerPlaintext
			if err := got.Unmarshal(raw); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got.Content, payload) || got.RealType != want.RealType || got.Zeros != zeros {
				t.Fatalf("round trip: got %+v, want %+v", got, want)
			}
		}
	}
}
