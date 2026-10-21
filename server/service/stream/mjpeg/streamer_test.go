package mjpeg

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type controlledResponseWriter struct {
	header    http.Header
	deadline  time.Time
	deadlines []time.Time
	flushErr  error
	writeSize int
}

func (w *controlledResponseWriter) Header() http.Header {
	return w.header
}

func (w *controlledResponseWriter) Write(data []byte) (int, error) {
	w.writeSize += len(data)
	return len(data), nil
}

func (w *controlledResponseWriter) WriteHeader(_ int) {}

func (w *controlledResponseWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	w.deadlines = append(w.deadlines, deadline)
	return nil
}

func (w *controlledResponseWriter) FlushError() error {
	return w.flushErr
}

func TestClientOfferKeepsNewestFrame(t *testing.T) {
	client := newMjpegClient(context.Background())
	client.offer([]byte("old"))
	client.offer([]byte("new"))

	frame, ok := client.next()
	if !ok {
		t.Fatal("client unexpectedly closed")
	}
	if got := string(frame); got != "new" {
		t.Fatalf("got frame %q, want newest frame", got)
	}
}

func TestClientNextStopsWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := newMjpegClient(ctx)
	cancel()

	if _, ok := client.next(); ok {
		t.Fatal("next returned a frame after cancellation")
	}
}

func TestStopIfIdleKeepsStreamerRunningWhenClientArrives(t *testing.T) {
	streamer := NewStreamer()
	client := newMjpegClient(context.Background())

	streamer.mutex.Lock()
	streamer.running = true
	streamer.clients[client] = struct{}{}
	streamer.updateClientSnapshotLocked()
	streamer.mutex.Unlock()

	if streamer.stopIfIdle() {
		t.Fatal("streamer stopped with an active client")
	}
	if !streamer.running {
		t.Fatal("streamer running flag was cleared with an active client")
	}
}

func TestStopIfIdleClearsRunningFlag(t *testing.T) {
	streamer := NewStreamer()
	streamer.running = true

	if !streamer.stopIfIdle() {
		t.Fatal("idle streamer did not stop")
	}
	if streamer.running {
		t.Fatal("idle streamer running flag was not cleared")
	}
}

func TestWriteFrameUsesUnderlyingDeadlineAndFlushError(t *testing.T) {
	flushErr := errors.New("flush failed")
	underlying := &controlledResponseWriter{
		header:   make(http.Header),
		flushErr: flushErr,
	}
	context, _ := gin.CreateTestContext(underlying)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/stream/mjpeg", nil)

	err := writeFrame(context, newResponseController(context.Writer), []byte("frame"), true)
	if !errors.Is(err, flushErr) {
		t.Fatalf("writeFrame error = %v, want %v", err, flushErr)
	}
	if underlying.deadline.IsZero() {
		t.Fatal("write deadline was not set on the underlying response writer")
	}
	if underlying.writeSize == 0 {
		t.Fatal("frame was not written before flush")
	}
}

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

func TestWriteFrameClearsDeadlineAfterFlush(t *testing.T) {
	underlying := &controlledResponseWriter{header: make(http.Header)}
	c, _ := gin.CreateTestContext(underlying)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/stream/mjpeg", nil)
	started := time.Now()
	if err := writeFrame(c, newResponseController(c.Writer), []byte("frame"), true); err != nil {
		t.Fatal(err)
	}
	if len(underlying.deadlines) != 2 {
		t.Fatalf("deadline changes = %v, want bounded write then clear", underlying.deadlines)
	}
	if deadline := underlying.deadlines[0]; deadline.Before(started.Add(clientWriteTimeout)) || deadline.After(time.Now().Add(clientWriteTimeout)) {
		t.Fatalf("write deadline = %v, want a %v write budget", deadline, clientWriteTimeout)
	}
	if !underlying.deadlines[1].IsZero() {
		t.Fatal("deadline remained active while waiting for another image")
	}
}

func TestMjpegRateNextToVideo(t *testing.T) {
	for _, c := range []struct {
		screen int
		video  bool
		wide   bool
		want   int
	}{
		{90, false, false, 90}, {90, true, false, 30}, {60, true, false, 30}, {30, true, false, 30},
		{27, true, false, 27}, {60, true, true, 25}, {25, true, true, 25}, {20, true, true, 20},
		{60, false, true, 60}, {0, true, false, 1}, {0, false, false, 1},
	} {
		if got := mjpegRate(c.screen, c.video, c.wide); got != c.want {
			t.Errorf("mjpegRate(%d, %v, %v) = %d, want %d", c.screen, c.video, c.wide, got, c.want)
		}
	}
}
