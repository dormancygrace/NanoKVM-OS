package common

import "errors"

// ErrPlanarPictureUnavailable means the native library cannot deliver planar
// pictures of the screen (kvmv_read_bgr_planar).
var ErrPlanarPictureUnavailable = errors.New("planar screen pictures are unavailable")

// ErrPlanarPictureTimeout means VPSS gave no picture within the native call's
// wait; a capture that has just started may need another try.
var ErrPlanarPictureTimeout = errors.New("no picture of the screen in time")

// ErrPlanarPictureNoSignal means no HDMI cable is connected.
var ErrPlanarPictureNoSignal = errors.New("no HDMI signal")

// ErrPlanarCaptureNotReady means the capture is changing (a new input size
// being detected) or cannot run at the input's resolution.
var ErrPlanarCaptureNotReady = errors.New("the capture is not ready")
