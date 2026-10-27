// Copyright 2026 NanoKVM OS contributors.
// Use of this source code is governed by the BSD-style LICENSE in this directory.

package websocket

import (
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// Complete the old proxy fixture's CONNECT using an actual tunnel. Recent
// net/http closes a CONNECT response unless its handler takes over the socket.
// Prepared-message tests also need GODEBUG=randseednop=0 to honor their seeds.
func c906ProxyTunnel(t *testing.T, w http.ResponseWriter, target string) {
	t.Helper()
	upstream, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		t.Error(err)
		http.Error(w, "proxy connection failed", http.StatusBadGateway)
		return
	}
	defer upstream.Close()
	downstream, rw, err := w.(http.Hijacker).Hijack()
	if err != nil {
		t.Error(err)
		return
	}
	defer downstream.Close()
	if _, err = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		t.Error(err)
		return
	}
	if err = rw.Flush(); err != nil {
		t.Error(err)
		return
	}
	done := make(chan struct{})
	go func() { io.Copy(upstream, rw); upstream.Close(); close(done) }()
	io.Copy(downstream, upstream)
	downstream.Close()
	<-done
}
