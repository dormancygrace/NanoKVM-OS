package webrtc

import (
	"github.com/pion/rtcp"
	"testing"
)

func TestVideoRefreshFeedback(t *testing.T) {
	for _, tc := range []struct {
		name    string
		packets []rtcp.Packet
		want    bool
	}{
		{"pli", []rtcp.Packet{&rtcp.PictureLossIndication{SenderSSRC: 1, MediaSSRC: 2}}, true},
		{"fir", []rtcp.Packet{&rtcp.FullIntraRequest{SenderSSRC: 1, FIR: []rtcp.FIREntry{{SSRC: 2, SequenceNumber: 1}}}}, true},
		{"receiver report", []rtcp.Packet{&rtcp.ReceiverReport{SSRC: 1}}, false},
		{"compound", []rtcp.Packet{&rtcp.ReceiverReport{SSRC: 1}, &rtcp.PictureLossIndication{SenderSSRC: 1, MediaSSRC: 2}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := rtcp.Marshal(tc.packets)
			if err != nil {
				t.Fatal(err)
			}
			if requestsVideoRefresh(b) != tc.want {
				t.Fatalf("refresh != %v", tc.want)
			}
		})
	}
	if requestsVideoRefresh([]byte{0x81, 0xce, 0xff}) {
		t.Fatal("malformed feedback accepted")
	}
}
