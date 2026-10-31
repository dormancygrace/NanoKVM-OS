package ws

import (
	"time"

	"github.com/gorilla/websocket"
)

// Browsers answer protocol Ping even while page JavaScript is suspended.
// Application heartbeats alone mistake that suspension for a dead connection.
func (c *Client) startTransportHeartbeat() func() {
	c.ws.SetPongHandler(func(string) error {
		c.inputMutex.Lock()
		safe := c.hold.enabled && !c.hold.closed
		c.inputMutex.Unlock()
		// Legacy clients without the independent held-input watchdog still
		// need the application timeout to release keys after a frozen page.
		// A transport Pong must never renew the held-input watchdog itself.
		if safe {
			return c.UpdateHeartbeat()
		}
		return nil
	})
	timeout := c.heartbeatTimeout
	if timeout <= 0 {
		timeout = clientHeartbeatTimeout
	}
	interval := timeout / 3
	if interval <= 0 {
		interval = time.Nanosecond
	}
	stop := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				// WriteControl is safe alongside the regular data writer.
				if err := c.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
					_ = c.ws.Close() // Wake the reader and release its input lease.
					return
				}
			}
		}
	}()
	return func() {
		close(stop)
		// Read owns the connection. Closing it also interrupts an in-flight
		// control write so joining the heartbeat worker cannot delay teardown.
		_ = c.ws.Close()
		<-stopped
	}
}
