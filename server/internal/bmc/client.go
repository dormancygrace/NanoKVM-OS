package bmc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// The server's internal token (despite the name, not PicoClaw's alone):
	// loopback callers that present it reach /api/internal.
	internalTokenFile   = "/etc/kvm/.picoclaw_internal_token"
	internalTokenHeader = "X-NanoKVM-Internal-Token"
	serverConfigFile    = "/etc/kvm/server.yaml"
)

// Client calls the server's ATX API over loopback HTTP. It speaks the few
// lines of HTTP/1.1 it needs itself: net/http would add some 2 MB to the
// IPMI daemon, which is all this package's code it runs.
type Client struct {
	Addr      string // 127.0.0.1:<port>
	TokenFile string
	// Timeout of one request beyond a press: the server answers a press
	// only when the button is released, so a press gets its hold on top.
	Timeout time.Duration
}

// NewClient finds the server's plain-HTTP port in server.yaml (80 when
// unset); that listener serves the internal routes also while HTTPS is on.
func NewClient() *Client {
	port := 80
	if data, err := os.ReadFile(serverConfigFile); err == nil {
		if p := httpPort(data); p > 0 {
			port = p
		}
	}
	return &Client{Addr: "127.0.0.1:" + strconv.Itoa(port), TokenFile: internalTokenFile, Timeout: 10 * time.Second}
}

// httpPort reads port.http from the YAML block the server writes:
//
//	port:
//	  http: 80
func httpPort(data []byte) int {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	inPort := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			inPort = strings.TrimSpace(line) == "port:"
			continue
		}
		if !inPort {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || key != "http" {
			continue
		}
		value, _, _ = strings.Cut(value, "#")
		if p, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && p > 0 && p < 65536 {
			return p
		}
	}
	return 0
}

// request sends one request on its own connection and returns the status
// code and body. hold is how long the server holds a button before it
// answers.
func (c *Client) request(ctx context.Context, method string, body []byte, hold time.Duration) (int, []byte, error) {
	token, err := os.ReadFile(c.TokenFile)
	if err != nil {
		return 0, nil, fmt.Errorf("read the internal token: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout+hold)
	defer cancel()
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", c.Addr)
	if err != nil {
		return 0, nil, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	var req bytes.Buffer
	fmt.Fprintf(&req, "%s %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n%s: %s\r\n",
		method, ATXPath, c.Addr, internalTokenHeader, strings.TrimSpace(string(token)))
	if body != nil {
		fmt.Fprintf(&req, "Content-Type: application/json\r\nContent-Length: %d\r\n", len(body))
	}
	req.WriteString("\r\n")
	req.Write(body)
	if _, err = conn.Write(req.Bytes()); err != nil {
		return 0, nil, err
	}
	return readResponse(bufio.NewReader(conn))
}

// readResponse reads a status line, headers and a body of Content-Length
// bytes (or up to the end of the connection), at most 64 KiB.
func readResponse(r *bufio.Reader) (int, []byte, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return 0, nil, err
	}
	fields := strings.Fields(line)
	if len(fields) < 2 || !strings.HasPrefix(fields[0], "HTTP/1.") {
		return 0, nil, errors.New("bad HTTP status line")
	}
	code, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, nil, errors.New("bad HTTP status code")
	}
	length := -1
	for {
		header, err := r.ReadString('\n')
		if err != nil {
			return 0, nil, err
		}
		header = strings.TrimRight(header, "\r\n")
		if header == "" {
			break
		}
		name, value, _ := strings.Cut(header, ":")
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			length, _ = strconv.Atoi(strings.TrimSpace(value))
		}
	}
	const limit = 64 << 10
	if length > limit {
		return 0, nil, errors.New("HTTP body too large")
	}
	if length >= 0 {
		body := make([]byte, length)
		_, err = io.ReadFull(r, body)
		return code, body, err
	}
	body, err := io.ReadAll(io.LimitReader(r, limit))
	return code, body, err
}

func (c *Client) State(ctx context.Context) (ATXState, error) {
	var state ATXState
	code, body, err := c.request(ctx, "GET", nil, 0)
	if err != nil {
		return state, err
	}
	if code != 200 {
		return state, fmt.Errorf("ATX state: HTTP %d", code)
	}
	err = json.Unmarshal(body, &state)
	return state, err
}

func (c *Client) Press(ctx context.Context, button string, durationMs int) error {
	body, err := json.Marshal(ATXPress{Button: button, DurationMs: durationMs})
	if err != nil {
		return err
	}
	code, _, err := c.request(ctx, "POST", body, time.Duration(durationMs)*time.Millisecond)
	if err != nil {
		return err
	}
	switch code {
	case 200, 204:
		return nil
	case 501:
		if button == "reset" {
			return ErrNoReset
		}
		return ErrNoATX
	default:
		return fmt.Errorf("ATX %s: HTTP %d", button, code)
	}
}
