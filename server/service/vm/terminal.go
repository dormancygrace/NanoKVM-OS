package vm

import (
	"NanoKVM-Server/middleware"
	"os/exec"

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

	cmd := exec.Command("/bin/sh", "-l")
	cmd.Dir = "/root"
	cmd.Env = append(cmd.Environ(), "HOME=/root")
	if err := runTerminalSession(ws, cmd); err != nil {
		log.Errorf("terminal session failed: %s", err)
	}
}
