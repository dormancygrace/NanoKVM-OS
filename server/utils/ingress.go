package utils

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
)

type connKey struct{}

// rememberConn keeps the accepted connection in the request context, for
// IngressInterface. NewHTTPServer installs it as ConnContext.
func rememberConn(ctx context.Context, conn net.Conn) context.Context {
	return context.WithValue(ctx, connKey{}, conn)
}

// IngressInterface names the interface a request's connection arrived on.
// The destination address does not tell: Linux accepts a packet for any of
// its addresses on every interface, so a host on Ethernet can reach the
// Wi-Fi access point's address by routing it through the device.
func IngressInterface(r *http.Request) (string, error) {
	conn, _ := r.Context().Value(connKey{}).(net.Conn)
	if tlsConn, ok := conn.(*tls.Conn); ok {
		conn = tlsConn.NetConn()
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
