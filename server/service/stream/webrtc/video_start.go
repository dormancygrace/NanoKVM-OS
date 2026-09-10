package webrtc

import (
	"github.com/pion/rtp"
	"sync/atomic"
)

// Each peer has its own decoder even when the manager shares one subscription.
// The manager serializes frame writes; publish readiness only after a whole
// keyframe was handed to the track successfully.
type videoStartGate struct{ started atomic.Bool }

func (g *videoStartGate) write(keyframe bool, packets []*rtp.Packet, write func([]*rtp.Packet) error) (bool, error) {
	if len(packets) == 0 || (!g.started.Load() && !keyframe) {
		return false, nil
	}
	if err := write(packets); err != nil {
		return true, err
	}
	g.started.Store(true)
	return true, nil
}
