package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"syscall"
	"time"
)

// The socket the tailscale client uses on Linux when /var/run exists.
var localAPISocket = "/var/run/tailscale/tailscaled.sock"

// errDaemonUnreachable means nothing listens on the socket: tailscale status
// fails the same way, so there is no point in starting it.
var errDaemonUnreachable = errors.New("tailscaled is not reachable")

const localAPITimeout = 10 * time.Second

// localStatus reads the status that tailscale status --json prints from
// tailscaled's local API, without starting the client, a large Go program.
func localStatus(socket string) (*TsStatus, error) {
	client := http.Client{Timeout: localAPITimeout, Transport: &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			conn, err := dialer.DialContext(ctx, "unix", socket)
			if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
				return nil, fmt.Errorf("%w: %w", errDaemonUnreachable, err)
			}
			return conn, err
		},
	}}
	// The local API only accepts this host name; the client leaves out peers
	// it does not need.
	response, err := client.Get("http://local-tailscaled.sock/localapi/v0/status?peers=false")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tailscaled local API: %s", response.Status)
	}
	var status TsStatus
	if err = json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&status); err != nil {
		return nil, err
	}
	return &status, nil
}
