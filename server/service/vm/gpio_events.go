package vm

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// GetGpioEvents streams LED snapshots under the same authentication as GetGpio.
// CheckToken cancels the request when its session expires or is revoked.
func (s *Service) GetGpioEvents(c *gin.Context) {
	if !ATXEnabled() {
		c.Status(http.StatusNoContent)
		return
	}
	streamGPIOEvents(c, gpioMonitor())
}

func streamGPIOEvents(c *gin.Context, monitor *gpioInputMonitor) {
	// Wait only for the first read, including a failed read. Errors are null
	// snapshots, and recovery arrives on this same connection.
	_, _ = monitor.Current(c.Request.Context())
	if c.Request.Context().Err() != nil {
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-store")
	c.Header("X-Accel-Buffering", "no")
	controller := http.NewResponseController(c.Writer)
	write := func(data string) bool {
		if err := controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return false
		}
		if _, err := c.Writer.WriteString(data); err != nil {
			return false
		}
		if err := controller.Flush(); err != nil {
			return false
		}
		// Do not leave an HTTP/2 deadline armed while waiting for an edge.
		err := controller.SetWriteDeadline(time.Time{})
		return err == nil || errors.Is(err, http.ErrNotSupported)
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		status, err, changed := monitor.snapshot()
		data := "data: null\n\n"
		if err == nil {
			data = fmt.Sprintf("data: {\"pwr\":%t,\"hdd\":%t}\n\n", status.Power, status.HDD)
		}
		if !write(data) {
			return
		}
	wait:
		for {
			select {
			case <-c.Request.Context().Done():
				return
			case <-monitor.stop:
				write("data: null\n\n")
				return
			case <-changed:
				break wait
			case <-heartbeat.C:
				if !write(": keepalive\n\n") {
					return
				}
			}
		}
	}
}
