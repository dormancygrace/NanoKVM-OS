package vm

import (
	"NanoKVM-Server/middleware"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  maxMessageSize,
	WriteBufferSize: maxMessageSize,
	CheckOrigin:     middleware.CheckWebSocketOrigin,
}

func (s *Service) Terminal(c *gin.Context) {
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		c.String(http.StatusBadRequest, "invalid terminal parameters")
		return
	}
	cmd, err := terminalCommand(query)
	if err != nil {
		c.String(http.StatusBadRequest, "%s", err.Error())
		return
	}
	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Errorf("failed to init websocket: %s", err)
		return
	}
	stopSessionWatcher := middleware.WatchWebSocket(c.Request.Context(), ws)
	defer stopSessionWatcher()
	defer func() {
		_ = ws.Close()
	}()

	cmd.Dir = "/root"
	cmd.Env = append(cmd.Environ(), "HOME=/root")
	if err := runTerminalSession(ws, cmd); err != nil {
		_ = ws.WriteMessage(websocket.BinaryMessage, []byte("Unable to start terminal session.\r\n"))
		log.Errorf("terminal session failed: %s", err)
	}
}
