package hid

import (
	"errors"

	"github.com/gin-gonic/gin"
)

// ErrInputOwnerChanged stops a paste or key sequence once another session
// has taken input control: its keystrokes would mix with the new owner's.
var ErrInputOwnerChanged = errors.New("another session took input control")

// InputOwnerCode is the response code of a request that lost, or never
// had, input control.
const InputOwnerCode = -4

const inputOwnerKey = "nanokvm.hid.inputOwner"

// isKeyboardRelease reports an all-up keyboard report: no modifier and no
// key (no character maps to key code 0).
func isKeyboardRelease(report []byte) bool {
	return report[0] == 0 && report[2] == 0
}

// SetInputOwner records, for one request, whether it still speaks for the
// session that holds input control. The router checks the lease once on
// entry; a paste runs for up to 25 s, so it asks again before every press.
func SetInputOwner(c *gin.Context, owns func() bool) {
	c.Set(inputOwnerKey, owns)
}

func inputOwner(c *gin.Context) func() bool {
	if value, ok := c.Get(inputOwnerKey); ok {
		if owns, ok := value.(func() bool); ok && owns != nil {
			return owns
		}
	}
	return func() bool { return true }
}
