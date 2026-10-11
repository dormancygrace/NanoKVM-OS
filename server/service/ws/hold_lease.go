package ws

import (
	"NanoKVM-Server/service/hid"
	"NanoKVM-Server/service/inputcontrol"
	log "github.com/sirupsen/logrus"
	"time"
)

const holdReleaseTimeout = 300 * time.Millisecond

// Protected by Client.inputMutex. Clients opt in with message 4; old clients
// keep the normal socket heartbeat behavior. No timer runs without held input.
type holdLease struct {
	enabled, closed bool
	held            [3]bool
	generation      uint64
	timer           *time.Timer
}

func (c *Client) keepHeldInputAlive() {
	c.inputMutex.Lock()
	defer c.inputMutex.Unlock()
	c.hold.enabled = true
	c.armHeldInputLocked()
}

func (c *Client) noteHeldInputLocked(kind inputcontrol.ManualReportKind, held bool) {
	if int(kind) >= len(c.hold.held) {
		return
	}
	c.hold.held[kind] = held
	c.armHeldInputLocked()
}

func (c *Client) armHeldInputLocked() {
	c.hold.generation++
	if c.hold.timer != nil {
		c.hold.timer.Stop()
		c.hold.timer = nil
	}
	if c.hold.closed || !c.hold.enabled || c.hold.held == [3]bool{} {
		return
	}
	generation := c.hold.generation
	c.hold.timer = time.AfterFunc(holdReleaseTimeout, func() { c.expireHeldInput(GetManager(), generation, hid.ReleaseAllHIDState) })
}

func (c *Client) expireHeldInput(manager *Manager, generation uint64, release func() error) {
	manager.controlMutex.Lock()
	defer manager.controlMutex.Unlock()
	c.inputMutex.Lock()
	defer c.inputMutex.Unlock()
	if c.hold.closed || generation != c.hold.generation || c.hold.held == [3]bool{} {
		return
	}
	c.hold.held = [3]bool{}
	c.armHeldInputLocked()
	if !manager.CanControl(c) || c.manual == nil {
		return
	}
	// Invalidate pending reports and release hardware before surrendering the
	// manual lease, so a jiggler, script or external controller cannot interleave.
	if err := c.manual.ReleaseHeld(release); err != nil {
		log.Warnf("failed to release input after missing keepalive: %v", err)
	}
}

func (c *Client) stopHeldInputWatch() {
	c.inputMutex.Lock()
	defer c.inputMutex.Unlock()
	c.hold.closed = true
	c.hold.held = [3]bool{}
	c.armHeldInputLocked()
}
