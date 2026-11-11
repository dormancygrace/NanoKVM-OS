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
// It serves one kernel connection, and while the session is opening one more
// after that: the mount tried again with the other cipher offer, which gets a
// new connection to the server. Reconnecting later requires a new ISO session.
type smbRelay struct {
	listener net.Listener
	mu       sync.Mutex
	upstream net.Conn
	client   net.Conn
	closed   bool
}

// opening ends when the session is open or has failed.
func newSMBRelay(opening context.Context, address string) (*smbRelay, error) {
	upstream, err := (&net.Dialer{}).DialContext(opening, "tcp", address)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		upstream.Close()
		return nil, err
	}
	r := &smbRelay{listener: listener, upstream: upstream}
	context.AfterFunc(opening, func() { listener.Close() })
	go func() {
		defer r.Close()
		for second := false; ; second = true {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			if second {
				listener.Close()
				if upstream, err = (&net.Dialer{}).DialContext(opening, "tcp", address); err != nil {
					client.Close()
					return
				}
			}
			r.mu.Lock()
			if r.closed {
				r.mu.Unlock()
				client.Close()
				upstream.Close()
				return
			}
			r.upstream, r.client = upstream, client
			r.mu.Unlock()
			go func(upstream net.Conn) {
				_, _ = io.Copy(client, upstream)
				client.Close()
			}(upstream)
			_, _ = io.Copy(upstream, client)
			upstream.Close()
			client.Close()
			if second || opening.Err() != nil {
				return
			}
		}
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
