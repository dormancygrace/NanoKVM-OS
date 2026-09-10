// Package rtpmtu enforces a peer's current RTP budget below retransmission caches.
package rtpmtu

import (
	"github.com/pion/interceptor"
	"github.com/pion/rtp"
	"sync/atomic"
)

type Factory struct {
	Budget  func() uint16
	Dropped atomic.Uint64
}

func (f *Factory) NewInterceptor(string) (interceptor.Interceptor, error) {
	return &guard{factory: f}, nil
}

type guard struct {
	interceptor.NoOp
	factory *Factory
}

func (g *guard) BindLocalStream(_ *interceptor.StreamInfo, writer interceptor.RTPWriter) interceptor.RTPWriter {
	return interceptor.RTPWriterFunc(func(h *rtp.Header, p []byte, a interceptor.Attributes) (int, error) {
		size := h.MarshalSize() + len(p)
		if g.factory.Budget != nil && size > int(g.factory.Budget()) {
			// Old cached NACK/RTX packets cannot be re-fragmented without changing RTP
			// semantics. Discard before SRTP; returning an error would tear down video.
			g.factory.Dropped.Add(1)
			return size, nil
		}
		return writer.Write(h, p, a)
	})
}
