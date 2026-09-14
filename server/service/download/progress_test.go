package download

import (
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTransferMeterResumeUnknownAndStall(t *testing.T) {
	start := time.Unix(10, 0)
	meter := &transferMeter{previousBytes: 1000, previousTime: start}
	p := meter.sample(1400, 2000, start.Add(2*time.Second))
	if p.BytesPerSecond != 200 || p.percentage() != "70.00%" {
		t.Fatalf("resumed bytes counted as transfer: %+v", p)
	}
	p = meter.sample(1400, 0, start.Add(3*time.Second))
	if p.BytesPerSecond != 0 || p.percentage() != "" {
		t.Fatalf("stalled unknown-length transfer: %+v", p)
	}
	p = meter.sample(1600, -1, start.Add(4*time.Second))
	if p.BytesPerSecond != 200 || p.Total != 0 {
		t.Fatalf("unknown size lost speed: %+v", p)
	}
}
func TestUnknownSizeWriterReportsBytes(t *testing.T) {
	var last transferProgress
	writer := newLoggingWriter(io.Discard, -1, func(p transferProgress) { last = p })
	writer.stopTicker()
	writer.Write([]byte("bytes"))
	writer.updateProgress()
	if last.Bytes != 5 || last.percentage() != "" {
		t.Fatalf("missing progress: %+v", last)
	}
}
func TestProgressAPIAndStaleUpdate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	done := make(chan struct{})
	service := &Service{downloadDone: done, downloadStatus: downloadStatusInProgress}
	service.setDownloadProgress(done, transferProgress{Bytes: 25, Total: 100, BytesPerSecond: 12.5})
	service.setDownloadProgress(make(chan struct{}), transferProgress{Bytes: 99, Total: 100})
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	service.StatusImage(ctx)
	var response struct {
		Data struct {
			Percentage      string
			DownloadedBytes int64
			TotalBytes      int64
			BytesPerSecond  float64
		}
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Percentage != "25.00%" || response.Data.DownloadedBytes != 25 || response.Data.TotalBytes != 100 || response.Data.BytesPerSecond != 12.5 {
		t.Fatalf("bad snapshot %s", recorder.Body.String())
	}
}

func TestCurlProgressWithoutContentLength(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		for i := 0; i < 7; i++ {
			w.Write(make([]byte, 4096))
			w.(http.Flusher).Flush()
			time.Sleep(200 * time.Millisecond)
		}
	}))
	defer server.Close()
	samples := make(chan transferProgress, 8)
	dst := curlDestination(t)
	err := downloadCurlImage(context.Background(), server.URL, dst, nil, func(p transferProgress) { samples <- p })
	if err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-samples:
		if p.Bytes <= 0 || p.BytesPerSecond <= 0 || p.Total != 0 || p.percentage() != "" {
			t.Fatalf("unknown-length curl progress: %+v", p)
		}
	default:
		t.Fatal("curl never reported bytes/speed without Content-Length")
	}
}
