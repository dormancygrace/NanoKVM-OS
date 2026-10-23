package webrtc

import (
	"NanoKVM-Server/service/stream"
	"sync"
	"time"

	"github.com/pion/rtp"
	log "github.com/sirupsen/logrus"
)

func (t *Track) writeVideoPackets(packets []*rtp.Packet) error {
	for _, packet := range packets {
		if err := t.video.WriteRTP(packet); err != nil {
			return err
		}
	}

	return nil
}

// sendErrorLogInterval bounds how often dropped video frames are logged.
const sendErrorLogInterval = 10 * time.Second

// sendErrorLog counts frames the batched UDP socket could not send.
type sendErrorLog struct {
	mu      sync.Mutex
	dropped int
	last    time.Time
}

func (l *sendErrorLog) note(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.dropped++
	now := time.Now()
	if !l.last.IsZero() && now.Sub(l.last) < sendErrorLogInterval {
		return
	}
	log.Warnf("WebRTC: %d video frame(s) not sent, the peer is kept: %v", l.dropped, err)
	l.dropped, l.last = 0, now
}

// writeFrame packetizes one frame for this peer and sends it. A send error
// from the batched UDP socket (a route gone for a moment during a Wi-Fi roam
// or VPN reconnect, EPERM from a firewall rule, EMSGSIZE after a lower path
// MTU) drops this frame only, as Pion does for unbatched writes: the browser
// sees the RTP gap and asks for a retransmission or a keyframe, and ICE
// decides whether the connection is gone. Tearing the peer down instead made
// the browser signal and gather again, maybe while the network was still
// down. A failing track write still ends the peer.
func (c *Client) writeFrame(sample stream.VideoFrame) error {
	packets := c.packetizer.packetize(sample.Data, sample.Timestamp, c.pathMTU.size())
	if len(packets) == 0 {
		return errVideoBudget
	}
	c.pathMTU.beginFrame()
	writeErr := c.track.writeVideoPackets(packets)
	batchErr := c.pathMTU.endFrame()
	if writeErr != nil {
		return writeErr
	}
	if batchErr != nil {
		c.sendErrors.note(batchErr)
	}
	return nil
}
