package remotemedia

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

type exchange struct {
	input  *bytes.Reader
	output bytes.Buffer
}

func (x *exchange) Read(p []byte) (int, error) { return x.input.Read(p) }
func (x *exchange) Write(p []byte) (int, error) {
	// Exercise partial successful writes too.
	if len(p) > 7 {
		p = p[:7]
	}
	return x.output.Write(p)
}
func request(cmd uint32, offset uint64, length uint32) []byte {
	b := make([]byte, 28)
	binary.BigEndian.PutUint32(b, requestMagic)
	binary.BigEndian.PutUint32(b[4:], cmd)
	copy(b[8:16], []byte{0, 255, 1, 254, 2, 253, 3, 252})
	binary.BigEndian.PutUint64(b[16:], offset)
	binary.BigEndian.PutUint32(b[24:], length)
	return b
}
func TestBlockReadsBoundsAndFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		offset uint64
		length uint32
		fail   bool
		errno  uint32
	}{
		{"start", 0, 2048, false, 0}, {"end", 63488, 2048, false, 0},
		{"past end", 65536, 512, false, 22}, {"overflow", ^uint64(0) - 1, 512, false, 22},
		{"read error", 0, 512, true, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := &exchange{input: bytes.NewReader(append(request(0, tc.offset, tc.length), request(2, 0, 0)...))}
			called := false
			err := Serve(x, 65536, func(offset uint64, length uint32) ([]byte, error) {
				called = true
				if tc.fail {
					return nil, errors.New("browser disconnected")
				}
				if offset != tc.offset || length != tc.length {
					t.Fatal("wrong browser range")
				}
				return bytes.Repeat([]byte{0xa5}, int(length)), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			b := x.output.Bytes()
			if len(b) < 16 || binary.BigEndian.Uint32(b) != replyMagic || binary.BigEndian.Uint32(b[4:]) != tc.errno {
				t.Fatalf("reply: %x", b)
			}
			if !bytes.Equal(b[8:16], request(0, 0, 0)[8:16]) {
				t.Fatal("changed opaque handle")
			}
			want := 16
			if tc.errno == 0 {
				want += int(tc.length)
			}
			if len(b) != want {
				t.Fatalf("reply length %d != %d", len(b), want)
			}
			if tc.errno == 22 && called {
				t.Fatal("out-of-bounds read reached browser")
			}
		})
	}
}
func TestRejectedWriteDoesNotDesynchronizeReads(t *testing.T) {
	b := append(request(1, 0, 512), make([]byte, 512)...)
	b = append(b, request(0, 0, 512)...)
	b = append(b, request(2, 0, 0)...)
	x := &exchange{input: bytes.NewReader(b)}
	if err := Serve(x, 65536, func(uint64, uint32) ([]byte, error) { return make([]byte, 512), nil }); err != nil {
		t.Fatal(err)
	}
	out := x.output.Bytes()
	if len(out) != 544 || binary.BigEndian.Uint32(out[4:]) != 30 || binary.BigEndian.Uint32(out[20:]) != 0 {
		t.Fatalf("bad write/read response: %x", out[:32])
	}
}
func TestMalformedAndShortReads(t *testing.T) {
	for _, req := range [][]byte{request(0, 0, MaxRead+1), make([]byte, 28), request(0, 0, 512)[:10]} {
		x := &exchange{input: bytes.NewReader(req)}
		if err := Serve(x, 65536, func(uint64, uint32) ([]byte, error) { t.Fatal("unexpected browser call"); return nil, nil }); err == nil {
			t.Fatal("accepted malformed request")
		}
	}
	x := &exchange{input: bytes.NewReader(request(0, 0, 512))}
	err := Serve(x, 65536, func(uint64, uint32) ([]byte, error) { return make([]byte, 511), nil })
	if !errors.Is(err, io.EOF) || binary.BigEndian.Uint32(x.output.Bytes()[4:]) != 5 {
		t.Fatal("short read was accepted")
	}
}
