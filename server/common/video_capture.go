package common

// VideoCaptureParams are the encoder request of one H.264/H.265 read.
type VideoCaptureParams struct {
	Width, Height uint16
	Codec         uint8
	BitRate       uint16
	GOP           uint8
	FPS           uint8
}

// VideoStream delivers access units paced by the native capture thread.
// Next and Update belong to one goroutine; Stop may be called from any
// goroutine and interrupts Next promptly. Close waits for the read in flight.
type VideoStream interface {
	Update(VideoCaptureParams)
	Next(headroom int) (storage []byte, data []byte, result int, ok bool)
	Stop()
	Close()
}
