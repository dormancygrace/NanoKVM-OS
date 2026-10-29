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

// ErrPlanarCaptureNotReady means a new input size is being detected.
var ErrPlanarCaptureNotReady = errors.New("the capture is not ready")

// ErrPlanarCaptureUnsupported means the input's resolution cannot be captured.
var ErrPlanarCaptureUnsupported = errors.New("the capture cannot run at the input's resolution")

// ErrPlanarInputChanged means the input is no longer the size asked for: a
// new size was detected; read it again.
var ErrPlanarInputChanged = errors.New("the input size changed")

// ErrPlanarInputTooLarge means the input is larger than Full HD.
var ErrPlanarInputTooLarge = errors.New("the input is larger than Full HD")
