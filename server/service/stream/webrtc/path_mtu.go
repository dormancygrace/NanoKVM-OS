package webrtc

import (
	"NanoKVM-Server/service/stream"
	"crypto/rand"
	"encoding/binary"
	"github.com/pion/ice/v4"
	"github.com/pion/rtp"
	log "github.com/sirupsen/logrus"
	"sync"
	"sync/atomic"
)

type frameBatcher interface {
	BeginFrame()
	EndFrame() error
}

type peerPathMTU struct {
	rtpMTU  atomic.Int32
	batchMu sync.RWMutex
	batch   frameBatcher
}

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

func (p *peerPathMTU) setBatcher(batch frameBatcher) {
	if p == nil {
		return
	}
	p.batchMu.Lock()
	p.batch = batch
	p.batchMu.Unlock()
}

func (p *peerPathMTU) beginFrame() {
	if p == nil {
		return
	}
	p.batchMu.RLock()
	batch := p.batch
	p.batchMu.RUnlock()
	if batch != nil {
		batch.BeginFrame()
	}
}

func (p *peerPathMTU) endFrame() error {
	if p == nil {
		return nil
	}
	p.batchMu.RLock()
	batch := p.batch
	p.batchMu.RUnlock()
	if batch == nil {
		return nil
	}
	return batch.EndFrame()
}

// Owned by one video writer. Sequence state and timestamp origin survive MTU
// changes; replacing a Pion packetizer alone would reset its random clock.
type adaptiveVideoPacketizer struct {
	codec     stream.VideoCodec
	sequence  rtp.Sequencer
	payloader rtp.Payloader
	origin    uint32
}

func newAdaptiveVideoPacketizer(codec stream.VideoCodec) *adaptiveVideoPacketizer {
	var seed [4]byte
	rand.Read(seed[:])
	return &adaptiveVideoPacketizer{codec: codec, sequence: rtp.NewRandomSequencer(), origin: binary.BigEndian.Uint32(seed[:])}
}
func (p *adaptiveVideoPacketizer) packetize(data []byte, timestamp int64, mtu uint16) []*rtp.Packet {
	// A budget below RTP + codec fragmentation headers cannot carry video.
	if mtu < 32 || len(data) == 0 {
		return nil
	}
	// Payloaders receive MTU per call; retain their parameter sets across PMTU changes.
	if p.payloader == nil {
		var payloader rtp.Payloader = &ownedH264Payloader{}
		if p.codec == stream.VideoCodecH265 {
			payloader = &h265Payloader{}
		}
		p.payloader = payloader
	}
	payloads := p.payloader.Payload(mtu-12, data)
	if len(payloads) == 0 {
		return nil
	}
	// One owned slab per frame instead of an allocation for every RTP header.
	// Never recycled: retransmission/interceptor consumers may retain packets.
	storage := make([]rtp.Packet, len(payloads))
	packets := make([]*rtp.Packet, len(payloads))
	stamp := p.origin + uint32(uint64(timestamp)*90/1000)
	for i, payload := range payloads {
		storage[i] = rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 100, SSRC: 0x1234ABCD,
			SequenceNumber: p.sequence.NextSequenceNumber(), Timestamp: stamp, Marker: i == len(payloads)-1}, Payload: payload}
		packets[i] = &storage[i]
	}
	return packets
}
