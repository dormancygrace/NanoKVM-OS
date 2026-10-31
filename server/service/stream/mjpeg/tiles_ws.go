package mjpeg

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/common"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/proto"
)

const (
	tilesPongWait   = 30 * time.Second
	tilesPingPeriod = tilesPongWait / 2
)

var tilesUpgrader = websocket.Upgrader{
	WriteBufferSize: 256 * 1024,
	CheckOrigin:     middleware.CheckWebSocketOrigin,
}

// ConnectTiles serves tiled MJPEG: one binary WebSocket message per changed
// picture (common/mjpeg_tiles.go), the first one whole.
func ConnectTiles(c *gin.Context) {
	if mjpegBlocked() {
		c.AbortWithStatusJSON(http.StatusConflict, proto.Response{Code: -3, Msg: common.MjpegBlockedMessage})
		return
	}
	ws, err := tilesUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Errorf("failed to upgrade tiled mjpeg to websocket: %s", err)
		return
	}
	defer ws.Close()
	stopSessionWatcher := middleware.WatchWebSocket(c.Request.Context(), ws)
	defer stopSessionWatcher()

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	client := streamer.AddTiledClient(ctx)
	defer streamer.RemoveClient(client)

	// The viewer sends nothing but control frames; a read error ends it.
	ws.SetReadLimit(64)
	_ = ws.SetReadDeadline(time.Now().Add(tilesPongWait))
	ws.SetPongHandler(func(string) error {
		return ws.SetReadDeadline(time.Now().Add(tilesPongWait))
	})
	go func() {
		defer cancel()
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}()

	ping := time.NewTicker(tilesPingPeriod)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			if err := ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(clientWriteTimeout)); err != nil {
				return
			}
		case message := <-client.frames:
			_ = ws.SetWriteDeadline(time.Now().Add(clientWriteTimeout))
			if err := ws.WriteMessage(websocket.BinaryMessage, message); err != nil {
				log.Debugf("failed to write tiled mjpeg to %s: %s", c.Request.RemoteAddr, err)
				return
			}
		}
	}
}
