package utils

import (
	"net/http"
	"time"
)

// NewHTTPServer limits idle connections and incomplete headers, while leaving
// streaming responses and ISO uploads without a global read/write deadline.
// HTTP/2 remains enabled by default; callers can disable it for a controlled
// TLS comparison. Browser WebSockets can negotiate a separate HTTP/1.1 socket.
func NewHTTPServer(addr string, handler http.Handler, http2 bool) *http.Server {
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(http2)
	return &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 2 * time.Minute, Protocols: protocols}
}
