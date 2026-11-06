package vm

import (
	"os"
	"path/filepath"
	"testing"

	"NanoKVM-Server/common"
	"NanoKVM-Server/service/stream"
)

func TestFirstCaptureYUV420(t *testing.T) {
	oldFile, oldEncoder, oldSizes, oldRates := transportFile, selectedEncoder, captureSizes, inputRates
	previous, previousFPS := common.GetScreen().Height, common.GetScreen().FPS
	t.Cleanup(func() {
		transportFile, selectedEncoder, captureSizes, inputRates = oldFile, oldEncoder, oldSizes, oldRates
		common.SetScreen("resolution", int(previous))
		common.SetScreen("fps", previousFPS)
	})
	transportFile = filepath.Join(t.TempDir(), "transport")

	h264 := stream.EncoderConfig{Codec: stream.VideoCodecH264}
	h265 := stream.EncoderConfig{Codec: stream.VideoCodecH265}
	cases := []struct {
		name          string
		transport     string
		config        stream.EncoderConfig
		selected      bool
		streamHeight  int
		input, target [2]int
		fps           int
		rates         [2]int // measured input rate, monitor profile rate
		want          bool
	}{
		{"webrtc h264 at input size", "webrtc", h264, true, 0, [2]int{1920, 1080}, [2]int{}, 90, [2]int{}, true},
		{"webrtc h265", "webrtc", h265, true, 0, [2]int{1920, 1080}, [2]int{}, 90, [2]int{}, false},
		{"no codec selected", "webrtc", stream.EncoderConfig{}, false, 0, [2]int{1920, 1080}, [2]int{}, 90, [2]int{}, false},
		{"direct transport", "direct", h264, true, 0, [2]int{1920, 1080}, [2]int{}, 90, [2]int{}, true},
		{"mjpeg transport", "mjpeg", h264, true, 0, [2]int{1920, 1080}, [2]int{}, 90, [2]int{}, false},
		{"stream limit scales", "webrtc", h264, true, 720, [2]int{1920, 1080}, [2]int{}, 90, [2]int{}, false},
		{"stream limit above input", "webrtc", h264, true, 1440, [2]int{1920, 1080}, [2]int{}, 90, [2]int{}, true},
		{"h264 blocked at 4k", "webrtc", h264, true, 0, [2]int{3840, 2160}, [2]int{}, 90, [2]int{}, false},
		{"no signal, monitor target", "webrtc", h264, true, 1080, [2]int{}, [2]int{1920, 1080}, 90, [2]int{}, true},
		{"no signal, monitor target scaled", "webrtc", h264, true, 720, [2]int{}, [2]int{1920, 1080}, 90, [2]int{}, false},
		{"no signal, no target, input size", "webrtc", h264, true, 0, [2]int{}, [2]int{}, 90, [2]int{}, true},
		{"no signal, no target, limit", "webrtc", h264, true, 1080, [2]int{}, [2]int{}, 90, [2]int{}, false},
		{"stream rate thins measured input", "webrtc", h264, true, 0, [2]int{1920, 1080}, [2]int{}, 30, [2]int{60, 0}, false},
		{"stream rate within tolerance", "webrtc", h264, true, 0, [2]int{1920, 1080}, [2]int{}, 48, [2]int{50, 0}, true},
		{"stream rate thins profile rate", "webrtc", h264, true, 0, [2]int{}, [2]int{1920, 1080}, 60, [2]int{0, 90}, false},
		{"stream rate thins assumed 60 Hz", "webrtc", h264, true, 0, [2]int{}, [2]int{}, 30, [2]int{}, false},
		{"stream rate matches assumed 60 Hz", "webrtc", h264, true, 0, [2]int{}, [2]int{}, 60, [2]int{}, true},
		{"capture rate limit thins", "webrtc", h264, true, 0, [2]int{2560, 1600}, [2]int{}, 90, [2]int{60, 0}, false},
		{"stream limit caps the rate", "webrtc", h264, true, 1440, [2]int{1920, 1080}, [2]int{}, 90, [2]int{90, 0}, false},
		{"stream limit 2160 caps 60 Hz", "webrtc", h264, true, 2160, [2]int{1920, 1080}, [2]int{}, 90, [2]int{60, 0}, false},
		{"stream rate just past the tolerance", "webrtc", h264, true, 0, [2]int{1920, 1080}, [2]int{}, 47, [2]int{50, 0}, false},
		{"h264 policy blocks a measured 4k30", "webrtc", h264, true, 0, [2]int{3840, 2160}, [2]int{}, 30, [2]int{30, 0}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := os.WriteFile(transportFile, []byte(c.transport+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			selectedEncoder = func() (stream.EncoderConfig, bool) { return c.config, c.selected }
			captureSizes = func() (int, int, int, int) { return c.input[0], c.input[1], c.target[0], c.target[1] }
			inputRates = func() (int, int) { return c.rates[0], c.rates[1] }
			common.SetScreen("resolution", c.streamHeight)
			common.SetScreen("fps", c.fps)
			if got := firstCaptureYUV420(); got != c.want {
				t.Fatalf("firstCaptureYUV420() = %v, want %v", got, c.want)
			}
		})
	}
}
