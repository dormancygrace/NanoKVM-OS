package common

import "sync/atomic"

var videoDisabled atomic.Bool

// DisableVideo is startup-only, before GetScreen or any native video call.
// A retry starts a fresh process instead of trying to revive a closed stack.
func DisableVideo()       { videoDisabled.Store(true) }
func VideoDisabled() bool { return videoDisabled.Load() }
