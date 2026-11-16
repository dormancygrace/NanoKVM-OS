package utils

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

// TLS handshake limits. A handshake costs the core an ECDHE exchange and a
// signature, so a burst of them stalls video and input. A slot is taken only
// once the ClientHello has arrived, so silent sockets hold none, and kept at
// most tlsSlotHold, so a client that stalls later frees it. Within
// tlsHandshakeTimeout of the accept a connection waits for a slot and
// completes its handshake, or is closed. A source that keeps completing
// ClientHellos and stalling can still occupy slots for tlsSlotHold each;
// the connection limits bound how many it can try.
const (
	maxTLSHandshakes    = 16
	tlsHandshakeTimeout = 10 * time.Second
	tlsSlotHold         = 5 * time.Second
)

var errTLSListenerClosed = errors.New("tls listener closed")

// TLSListener completes the handshake of each accepted connection in its own
// goroutine, at most maxTLSHandshakes at a time, and returns only finished
// *tls.Conn. Accept is never blocked by a slow client.
//
// It replaces the lazy handshake of Server.ServeTLS rather than wrapping it,
// because the end of a handshake there is not visible to a listener or a
// tls.Config hook. ALPN and HTTP/2 are unaffected: config must list "h2" in
// NextProtos and be the http.Server's TLSConfig as well, so Serve sets up
// HTTP/2; net/http then reads the negotiated protocol from the finished
// connection, and its own HandshakeContext call returns at once.
func TLSListener(inner net.Listener, config *tls.Config) net.Listener {
	l := &tlsListener{
		inner: inner,
		conns: make(chan net.Conn),
		errs:  make(chan error),
		done:  make(chan struct{}),
		slots: make(chan struct{}, maxTLSHandshakes),
	}
	l.config = config.Clone()
	l.config.GetConfigForClient = l.takeSlot
	go l.acceptLoop()
	return l
}

type tlsListener struct {
	inner     net.Listener
	config    *tls.Config
	conns     chan net.Conn
	errs      chan error
	done      chan struct{}
	closeOnce sync.Once
	slots     chan struct{}
	holding   sync.Map // net.Conn given to tls.Server -> struct{}, while it holds a slot
}

func (l *tlsListener) acceptLoop() {
	for {
		conn, err := l.inner.Accept()
		if err != nil {
			// Serve backs off and calls Accept again after a temporary
			// error (EMFILE), so pass each error on and keep accepting.
			select {
			case l.errs <- err:
			case <-l.done:
				return
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		go l.handshake(conn)
	}
}

func (l *tlsListener) handshake(conn net.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), tlsHandshakeTimeout)
	defer cancel()
	tc := tls.Server(conn, l.config)
	err := tc.HandshakeContext(ctx)
	if _, held := l.holding.LoadAndDelete(conn); held {
		<-l.slots
	}
	if err != nil {
		replyPlainHTTP(err)
		_ = tc.Close()
		return
	}
	_ = conn.SetDeadline(time.Time{})
	select {
	case l.conns <- tc:
	case <-l.done:
		_ = tc.Close()
	}
}

// takeSlot runs once the ClientHello is read; nil keeps l.config.
func (l *tlsListener) takeSlot(hello *tls.ClientHelloInfo) (*tls.Config, error) {
	select {
	case l.slots <- struct{}{}:
	case <-hello.Context().Done():
		return nil, hello.Context().Err()
	case <-l.done:
		return nil, errTLSListenerClosed
	}
	l.holding.Store(hello.Conn, struct{}{})
	_ = hello.Conn.SetDeadline(time.Now().Add(tlsSlotHold))
	return nil, nil
}

// replyPlainHTTP answers a plain HTTP request sent to the HTTPS port as
// net/http does.
func replyPlainHTTP(err error) {
	var re tls.RecordHeaderError
	if !errors.As(err, &re) || re.Conn == nil {
		return
	}
	switch string(re.RecordHeader[:]) {
	case "GET /", "HEAD ", "POST ", "PUT /", "OPTIO":
		_, _ = io.WriteString(re.Conn, "HTTP/1.0 400 Bad Request\r\n\r\nClient sent an HTTP request to an HTTPS server.\n")
	}
}

func (l *tlsListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case err := <-l.errs:
		return nil, err
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *tlsListener) Close() error {
	l.closeOnce.Do(func() { close(l.done) })
	return l.inner.Close()
}

func (l *tlsListener) Addr() net.Addr { return l.inner.Addr() }
