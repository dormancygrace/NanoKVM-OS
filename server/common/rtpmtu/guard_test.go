package rtpmtu

import (
	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/nack"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"sync/atomic"
	"testing"
	"time"
)

func TestCachedNACKObeysReducedBudget(t *testing.T) {
	t.Run("NACK", func(t *testing.T) { checkCachedNACKBudget(t, 0) })
	t.Run("RTX", func(t *testing.T) { checkCachedNACKBudget(t, 8) })
}
func checkCachedNACKBudget(t *testing.T, rtxSSRC uint32) {
	var budget atomic.Uint32
	budget.Store(1456)
	f := &Factory{Budget: func() uint16 { return uint16(budget.Load()) }}
	registry := &interceptor.Registry{}
	registry.Add(f)
	responder, err := nack.NewResponderInterceptor()
	if err != nil {
		t.Fatal(err)
	}
	registry.Add(responder)
	chain, err := registry.Build("test")
	if err != nil {
		t.Fatal(err)
	}
	defer chain.Close()
	var writes atomic.Int32
	writer := chain.BindLocalStream(&interceptor.StreamInfo{SSRC: 7, SSRCRetransmission: rtxSSRC, PayloadTypeRetransmission: 98, RTCPFeedback: []interceptor.RTCPFeedback{{Type: "nack"}}}, interceptor.RTPWriterFunc(func(h *rtp.Header, p []byte, _ interceptor.Attributes) (int, error) {
		writes.Add(1)
		return h.MarshalSize() + len(p), nil
	}))
	h := &rtp.Header{Version: 2, SSRC: 7, SequenceNumber: 42}
	if _, err = writer.Write(h, make([]byte, 1200), nil); err != nil {
		t.Fatal(err)
	}
	if writes.Load() != 1 {
		t.Fatal("initial packet not sent")
	}
	budget.Store(1000)
	raw, err := rtcp.Marshal([]rtcp.Packet{&rtcp.TransportLayerNack{SenderSSRC: 9, MediaSSRC: 7, Nacks: []rtcp.NackPair{{PacketID: 42}}}})
	if err != nil {
		t.Fatal(err)
	}
	reader := chain.BindRTCPReader(interceptor.RTCPReaderFunc(func(b []byte, a interceptor.Attributes) (int, interceptor.Attributes, error) {
		return copy(b, raw), a, nil
	}))
	if _, _, err = reader.Read(make([]byte, 1500), nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for f.Dropped.Load() == 0 {
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("cached NACK bypassed guard")
		}
	}
	if writes.Load() != 1 {
		t.Fatal("oversized cached packet reached SRTP writer")
	}
	h.SequenceNumber++
	if _, err = writer.Write(h, make([]byte, 500), nil); err != nil {
		t.Fatal(err)
	}
	if writes.Load() != 2 {
		t.Fatal("in-budget packet rejected")
	}
}
