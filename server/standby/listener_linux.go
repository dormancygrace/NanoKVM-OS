package standby

import (
	"crypto/tls"
	"net"

	"golang.org/x/net/netutil"
)

// TLS must be the outer listener so net/http receives *tls.Conn and populates
// Request.TLS. Limiting the TCP listener still caps both handshakes and HTTP.
func runtimeListener(tcp net.Listener, c Config) (net.Listener, error) {
	limited := netutil.LimitListener(tcp, 32)
	if len(c.Certificate) == 0 {
		return limited, nil
	}
	cert, err := tls.X509KeyPair(c.Certificate, c.PrivateKey)
	if err != nil {
		limited.Close()
		return nil, err
	}
	return tls.NewListener(limited, &tls.Config{
		Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12,
	}), nil
}
