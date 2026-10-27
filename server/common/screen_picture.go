package common

import "errors"

// ErrPlanarPictureUnavailable means the native library cannot deliver planar
// pictures of the screen (kvmv_read_bgr_planar).
var ErrPlanarPictureUnavailable = errors.New("planar screen pictures are unavailable")
