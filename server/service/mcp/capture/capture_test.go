package mcpcapture

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"sync"
	"testing"

	mcpservice "NanoKVM-Server/service/mcp"
)

func testJPEG(t *testing.T, width int, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.White)
	var output bytes.Buffer
	if err := jpeg.Encode(&output, img, nil); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

type visionResponse struct {
	data   []byte
	result int
}

type fakeVision struct {
	mu        sync.Mutex
	responses []visionResponse
	calls     int
	quality   uint16
	width     uint16
	height    uint16
}

func (v *fakeVision) ReadMjpeg(width uint16, height uint16, quality uint16) ([]byte, int) {
	v.mu.Lock()
	v.calls++
	v.quality = quality
	v.width = width
	v.height = height
	index := v.calls - 1
	response := visionResponse{result: -1}
	if index < len(v.responses) {
		response = v.responses[index]
	} else if len(v.responses) > 0 {
		response = v.responses[len(v.responses)-1]
	}
	v.mu.Unlock()

	return response.data, response.result
}

func TestCaptureSuccessAndQualityBounds(t *testing.T) {
	vision := &fakeVision{responses: []visionResponse{{data: testJPEG(t, 1920, 1080), result: 0}}}
	snapshotter := NewWithCaptureLease(vision, func() (uint16, uint16) { return 1920, 1080 }, nil)

	snapshot, err := snapshotter.Capture(context.Background(), mcpservice.SnapshotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.OK || snapshot.Width != 1920 || snapshot.Height != 1080 || vision.quality != defaultQuality || vision.width != 1920 || vision.height != 1080 {
		t.Fatalf("snapshot=%+v capture=%dx%d quality=%d", snapshot, vision.width, vision.height, vision.quality)
	}

	_, err = snapshotter.Capture(context.Background(), mcpservice.SnapshotRequest{Quality: 200})
	if err != nil || vision.quality != maximumQuality {
		t.Fatalf("quality clamp=%d err=%v", vision.quality, err)
	}
}

func TestCaptureRejectsCropAndEmptyData(t *testing.T) {
	vision := &fakeVision{responses: []visionResponse{{result: 0}}}
	snapshotter := NewWithCaptureLease(vision, func() (uint16, uint16) { return 800, 600 }, nil)
	if _, err := snapshotter.Capture(context.Background(), mcpservice.SnapshotRequest{W: 10}); err == nil {
		t.Fatal("expected crop error")
	}
	if vision.calls != 0 {
		t.Fatalf("vision called for rejected crop: %d", vision.calls)
	}

	snapshot, err := snapshotter.Capture(context.Background(), mcpservice.SnapshotRequest{})
	if err != nil || snapshot.OK || snapshot.Message == "" {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
}
