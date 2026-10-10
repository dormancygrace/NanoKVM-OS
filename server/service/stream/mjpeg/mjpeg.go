package mjpeg

import (
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/common"
	"NanoKVM-Server/proto"
)

var streamer = NewStreamer()

var mjpegAllowed = common.MjpegAllowed

// blockedLogged keeps the log to one line per blocked period.
var blockedLogged atomic.Bool

// mjpegBlocked reports whether MJPEG is unavailable now. It logs once at
// Info when it becomes so, and again after it was available in between.
func mjpegBlocked() bool {
	if mjpegAllowed() {
		blockedLogged.Store(false)
		return false
	}
	if !blockedLogged.Swap(true) {
		log.Info(common.MjpegBlockedMessage)
	}
	return true
}

type LatestFrame struct {
	Data       []byte
	Width      uint16
	Height     uint16
	CapturedAt time.Time
}

func Connect(c *gin.Context) {
	if mjpegBlocked() {
		c.AbortWithStatusJSON(http.StatusConflict, proto.Response{Code: -3, Msg: common.MjpegBlockedMessage})
		return
	}

	c.Header("Content-Type", "multipart/x-mixed-replace; boundary=frame")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Pragma", "no-cache")
	c.Header("X-Server-Date", time.Now().Format(time.RFC1123))

	client := streamer.AddClient(c)
	defer streamer.RemoveClient(client)
	controller := newResponseController(c.Writer)

	first := true
	for {
		data, ok := client.next()
		if !ok {
			return
		}

		if err := writeFrame(c, controller, data, first); err != nil {
			log.Errorf("failed to write mjpeg frame for client %s: %s", c.Request.RemoteAddr, err)
			return
		}
		first = false
	}
}

func newResponseController(writer http.ResponseWriter) *http.ResponseController {
	if unwrapper, ok := writer.(interface{ Unwrap() http.ResponseWriter }); ok {
		writer = unwrapper.Unwrap()
	}

	return http.NewResponseController(writer)
}

func GetLatestFrame() (LatestFrame, bool) {
	return streamer.getLatestFrame()
}

func EnableLatestFrameCache() {
	streamer.enableLatestFrameCache()
}

func DisableLatestFrameCache() {
	streamer.disableLatestFrameCache()
}
