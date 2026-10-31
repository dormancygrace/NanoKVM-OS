package notify

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"time"
)

// syslogFacility is "daemon".
const syslogFacility = 3

// sendSyslog sends one RFC 5424 message: a datagram over UDP, octet-counted
// (RFC 6587) over TCP and TLS. MSGID is the event's kind.
func sendSyslog(ctx context.Context, ch Channel, r Rendered) error {
	severity := map[Severity]int{SeverityInfo: 6, SeverityWarning: 4, SeverityCritical: 2}[r.Severity]
	text := strings.Join(strings.Fields(strings.ReplaceAll(r.Title+": "+r.Body, "\n", " | ")), " ")
	msg := fmt.Sprintf("<%d>1 %s %s nanokvm - %s - %s",
		syslogFacility*8+severity, r.Time.Format(time.RFC3339), syslogName(r.Device), r.Kind, text)

	addr := net.JoinHostPort(ch.get("host"), ch.get("port"))
	dialer := &net.Dialer{}
	var conn net.Conn
	var err error
	switch ch.get("protocol") {
	case "tls":
		config := tlsConfig(ch)
		config.ServerName = ch.get("host")
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: config}).DialContext(ctx, "tcp", addr)
	case "tcp":
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	default:
		conn, err = dialer.DialContext(ctx, "udp", addr)
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if ch.get("protocol") != "udp" {
		msg = fmt.Sprintf("%d %s", len(msg), msg)
	}
	_, err = conn.Write([]byte(msg))
	return err
}

// syslogName is HOSTNAME of RFC 5424: printable ASCII without spaces.
func syslogName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r > 32 && r < 127 {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "-"
	}
	return b.String()
}
