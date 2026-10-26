package remotemedia

import (
	"context"
	"io"
	"net"
	"sync"
)

// The relay owns the TCP connection, while CIFS still owns the SMB protocol and
// crypto. Closing it breaks a kernel read/close immediately, including on server
// process exit. TCP-to-TCP io.Copy uses Linux splice rather than payload buffers.
// It accepts one kernel connection only; reconnecting requires a new ISO session.
type smbRelay struct {
	listener net.Listener
	upstream net.Conn
	mu       sync.Mutex
	client   net.Conn
	closed   bool
}

func newSMBRelay(ctx context.Context, address string) (*smbRelay, error) {
	upstream, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		upstream.Close()
		return nil, err
	}
	r := &smbRelay{listener: listener, upstream: upstream}
	go func() {
		client, err := listener.Accept()
		listener.Close()
		if err != nil {
			r.Close()
			return
		}
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			client.Close()
			return
		}
		r.client = client
		r.mu.Unlock()
		go func() { _, _ = io.Copy(upstream, client); r.Close() }()
		_, _ = io.Copy(client, upstream)
		r.Close()
	}()
	return r, nil
}

func (r *smbRelay) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	r.listener.Close()
	r.upstream.Close()
	if r.client != nil {
		r.client.Close()
	}
}
