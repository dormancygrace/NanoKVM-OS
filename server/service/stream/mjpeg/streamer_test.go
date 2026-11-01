package mjpeg

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestWriteFrameAdvancesHeadersBeforeNextImage(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/stream/mjpeg", nil)
	controller := newResponseController(c.Writer)
	frames := [][]byte{{0xff, 0xd8, 1, 0xff, 0xd9}, {0xff, 0xd8, 2, 0xff, 0xd9}, {0xff, 0xd8, 3, 0xff, 0xd9}}
	for i, frame := range frames {
		before := recorder.Body.Len()
		if err := writeFrame(c, controller, frame, i == 0); err != nil {
			t.Fatal(err)
		}
		flushed := recorder.Body.Bytes()[before:]
		if !recorder.Flushed || !bytes.HasSuffix(flushed, mjpegNextPart) {
			t.Fatal("a flushed JPEG must include the complete following headers")
		}
		if bytes.Contains(flushed, []byte("Content-Length:")) {
			t.Fatal("advance headers cannot advertise an unknown next JPEG length")
		}
	}
	reader := multipart.NewReader(strings.NewReader(recorder.Body.String()), "frame")
	for _, want := range frames {
		part, err := reader.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		if part.Header.Get("Content-Type") != "image/jpeg" {
			t.Fatal(part.Header)
		}
		got, err := io.ReadAll(part)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("parsed JPEG %x, want %x", got, want)
		}
	}
}

// Deduplication may leave the connection idle longer than a frame's write
// deadline. HTTP/2 must not reset the stream before another image arrives.
func TestWriteFrameHTTP2SurvivesIdleBetweenFrames(t *testing.T) {
	frames := [][]byte{{0xff, 0xd8, 1, 0xff, 0xd9}, {0xff, 0xd8, 2, 0xff, 0xd9}}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, _ := gin.CreateTestContext(w)
		c.Request = r
		c.Header("Content-Type", "multipart/x-mixed-replace; boundary=frame")
		controller := newResponseController(c.Writer)
		if err := writeFrame(c, controller, frames[0], true); err != nil {
			t.Errorf("first frame: %v", err)
			return
		}
		timer := time.NewTimer(clientWriteTimeout + 250*time.Millisecond)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
		}
		if err := writeFrame(c, controller, frames[1], false); err != nil {
			t.Errorf("second frame after idle: %v", err)
		}
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	client := server.Client()
	client.Timeout = 3 * clientWriteTimeout
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.ProtoMajor != 2 {
		t.Fatalf("negotiated %s, want HTTP/2", response.Proto)
	}
	reader := multipart.NewReader(response.Body, "frame")
	for i, want := range frames {
		part, err := reader.NextPart()
		if err != nil {
			t.Fatalf("frame %d headers: %v", i, err)
		}
		got, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("frame %d body: %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("frame %d = %x, want %x", i, got, want)
		}
	}
}

func TestMjpegRateNextToVideo(t *testing.T) {
	for _, c := range []struct {
		screen int
		video  bool
		want   int
	}{
		{120, false, 120}, {90, false, 90}, {60, false, 60},
		{120, true, 30}, {90, true, 30}, {60, true, 30}, {31, true, 30}, {30, true, 30},
		{29, true, 29}, {27, true, 27}, {25, true, 25}, {20, true, 20},
		{0, true, 1}, {0, false, 1}, {-1, true, 1},
	} {
		if got := mjpegRate(c.screen, c.video); got != c.want {
			t.Errorf("mjpegRate(%d, %v) = %d, want %d", c.screen, c.video, got, c.want)
		}
	}
}
