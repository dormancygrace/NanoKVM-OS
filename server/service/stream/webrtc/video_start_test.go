package webrtc

import (
	"errors"
	"github.com/pion/rtp"
	"testing"
)

func TestVideoStartGateIsPerPeerAndRequiresSuccessfulKeyframe(t *testing.T) {
	var established, late videoStartGate
	packets := []*rtp.Packet{{}}
	calls := 0
	write := func([]*rtp.Packet) error { calls++; return nil }
	if sent, err := established.write(true, packets, write); !sent || err != nil {
		t.Fatal("initial keyframe", sent, err)
	}
	if sent, _ := established.write(false, packets, write); !sent {
		t.Fatal("established peer lost delta frame")
	}
	if sent, err := late.write(false, packets, write); sent || err != nil || calls != 2 {
		t.Fatal("late peer received delta before keyframe")
	}
	if sent, _ := late.write(true, nil, write); sent {
		t.Fatal("empty keyframe opened gate")
	}
	boom := errors.New("track failed")
	if sent, err := late.write(true, packets, func([]*rtp.Packet) error { return boom }); !sent || !errors.Is(err, boom) {
		t.Fatal("write error not propagated")
	}
	if sent, _ := late.write(false, packets, write); sent {
		t.Fatal("failed keyframe opened gate")
	}
	if sent, err := late.write(true, packets, write); !sent || err != nil {
		t.Fatal("late peer rejected keyframe", err)
	}
	if sent, _ := late.write(false, packets, write); !sent || calls != 4 {
		t.Fatal("late peer did not continue after keyframe")
	}
}
