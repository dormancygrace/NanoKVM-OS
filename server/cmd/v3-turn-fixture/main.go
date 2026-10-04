//go:build v3oracle

// Test-only loopback TURN oracle. Never part of the replacement runtime/package.
package main

import (
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/pion/turn/v5"
)

func main() {
	output := os.Getenv("V3_TURN_READY")
	if output == "" {
		panic("V3_TURN_READY required")
	}
	listener, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	defer listener.Close()
	server, err := turn.NewServer(turn.ServerConfig{
		Realm: "v3-test-only",
		AuthHandler: func(request *turn.RequestAttributes) (string, []byte, bool) {
			if request.Username != "v3-test" {
				return "", nil, false
			}
			return request.Username, turn.GenerateAuthKey(request.Username, "v3-test-only", "test-only-password"), true
		},
		PacketConnConfigs: []turn.PacketConnConfig{{
			PacketConn:        listener,
			PermissionHandler: func(_ net.Addr, peer net.IP) bool { return peer.IsLoopback() },
			RelayAddressGenerator: &turn.RelayAddressGeneratorStatic{
				RelayAddress: net.ParseIP("127.0.0.1"), Address: "127.0.0.1",
			},
		}},
	})
	if err != nil {
		panic(err)
	}
	defer server.Close()
	if err := os.WriteFile(output, []byte(listener.LocalAddr().String()), 0600); err != nil {
		panic(err)
	}
	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, syscall.SIGINT, syscall.SIGTERM)
	<-stopped
}
