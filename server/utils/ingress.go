package utils

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"sync"
)

type connKey struct{}

// connIngress is one accepted connection and, once asked, its ingress
// interface: a connection keeps the interface it arrived on, and the name
// costs a netlink lookup that every request should not repeat.
type connIngress struct {
	conn net.Conn
	once sync.Once
	name string
	err  error
}

// rememberConn keeps the accepted connection in the request context, for
// IngressInterface. NewHTTPServer installs it as ConnContext.
func rememberConn(ctx context.Context, conn net.Conn) context.Context {
	return context.WithValue(ctx, connKey{}, &connIngress{conn: conn})
}

// IngressInterface names the interface a request's connection arrived on.
// The destination address does not tell: Linux accepts a packet for any of
// its addresses on every interface, so a host on Ethernet can reach the
// Wi-Fi access point's address by routing it through the device.
func IngressInterface(r *http.Request) (string, error) {
	c, _ := r.Context().Value(connKey{}).(*connIngress)
	if c == nil {
		return "", errors.New("not a TCP connection")
	}
	c.once.Do(func() { c.name, c.err = ingressName(c.conn) })
	return c.name, c.err
}

func ingressName(conn net.Conn) (string, error) {
	if tlsConn, ok := conn.(*tls.Conn); ok {
		conn = tlsConn.NetConn()
	}
	if wrapped, ok := conn.(interface{ TCP() *net.TCPConn }); ok {
		conn = wrapped.TCP()
	}
	tcp, ok := conn.(*net.TCPConn)
	if !ok {
		return "", errors.New("not a TCP connection")
	}
	index, err := ingressIndex(tcp)
	if err != nil {
		return "", err
	}
	iface, err := net.InterfaceByIndex(index)
	if err != nil {
		return "", err
	}
	return iface.Name, nil
}
