package webrtc

import (
	"NanoKVM-Server/service/stream"
	"github.com/pion/ice/v4"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	log "github.com/sirupsen/logrus"
	"sync/atomic"
)

type peerPathMTU struct{ rtpMTU atomic.Int32 }

func newPeerPathMTU() *peerPathMTU {
	p := &peerPathMTU{}
	p.rtpMTU.Store(videoRTPMTU)
	return p
}
func (p *peerPathMTU) receive(result ice.PathMTUResult) {
	// Reserve the largest profile we negotiate. RTP MTU includes its own header.
	mtu := int32(result.UDPSize - 16)
	if mtu < 0 {
		mtu = 0
	}
	previous := p.rtpMTU.Swap(mtu)
	if previous != mtu || result.Reason == "path-reset" {
		log.Infof("WebRTC path budget: RTP=%d confirmed=%t reason=%s", mtu, result.Confirmed, result.Reason)
	}
}
func (p *peerPathMTU) size() uint16 {
	if p == nil {
		return videoRTPMTU
	}
	return uint16(p.rtpMTU.Load())
}

// Owned by one video writer. Sequence state and timestamp origin survive MTU
// changes; replacing a Pion packetizer alone would reset its random clock.
type adaptiveVideoPacketizer struct {
	codec      stream.VideoCodec
	mtu        uint16
	sequence   rtp.Sequencer
	packetizer rtp.Packetizer
	origin     uint32
	haveOrigin bool
}

func newAdaptiveVideoPacketizer(codec stream.VideoCodec) *adaptiveVideoPacketizer {
	return &adaptiveVideoPacketizer{codec: codec, sequence: rtp.NewRandomSequencer()}
}
func (p *adaptiveVideoPacketizer) packetize(data []byte, timestamp int64, mtu uint16) []*rtp.Packet {
	// A budget below RTP + codec fragmentation headers cannot carry video.
	if mtu < 32 {
		return nil
	}
	if p.packetizer == nil || p.mtu != mtu {
		var payloader rtp.Payloader = &codecs.H264Payloader{}
		if p.codec == stream.VideoCodecH265 {
			payloader = &codecs.H265Payloader{}
		}
		p.packetizer = rtp.NewPacketizer(mtu, 100, 0x1234ABCD, payloader, p.sequence, 90000)
		p.mtu = mtu
	}
	packets := p.packetizer.Packetize(data, 0)
	if len(packets) == 0 {
		return packets
	}
	if !p.haveOrigin {
		p.origin = packets[0].Timestamp
		p.haveOrigin = true
	}
	stamp := p.origin + uint32(uint64(timestamp)*90/1000)
	for _, packet := range packets {
		packet.Timestamp = stamp
	}
	return packets
}
