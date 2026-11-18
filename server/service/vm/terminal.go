package vm

import (
	"errors"
	"net/http"
	"net/url"

	"NanoKVM-Server/middleware"
	"NanoKVM-Server/proto"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  maxMessageSize,
	WriteBufferSize: maxMessageSize,
	CheckOrigin:     middleware.CheckWebSocketOrigin,
}

// Terminal attaches the page to a shell: a new one, or with session=ID the
// user's shell that is still running (see terminal_session.go). With port=...
// it is a serial terminal instead, which ends with its page.
func (s *Service) Terminal(c *gin.Context) {
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		c.String(http.StatusBadRequest, "invalid terminal parameters")
		return
	}
	var serial *serialSettings
	if query.Has("port") {
		if serial, err = serialSettingsFromQuery(query); err != nil {
			c.String(http.StatusBadRequest, "%s", err.Error())
			return
		}
	}
	account := middleware.CurrentAccount(c)
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

	if serial != nil {
		if err := runSerialSession(ws, serial); err != nil {
			log.Errorf("serial terminal %s: %s", serial.Device, err)
		}
		return
	}

	var sh *shell
	started := false
	if id := query.Get("session"); id != "" {
		// Only the sign-in that started a shell attaches to it: the same
		// user is not enough once their account changed or they signed in
		// again after a revocation.
		if sh = shells.get(id); sh == nil || !sh.account.Same(account) {
			closeWebSocket(ws, shellNotFoundCode, "terminal session not found")
			return
		}
	} else if sh, err = shells.start(account); err != nil {
		if errors.Is(err, errShellLimit) {
			closeWebSocket(ws, shellLimitCode, "too many terminal sessions")
			return
		}
		_ = ws.WriteMessage(websocket.BinaryMessage, []byte("Unable to start terminal session.\r\n"))
		log.Errorf("terminal session failed: %s", err)
		return
	} else {
		started = true
	}
	if err := sh.attach(ws, middleware.ClientAddress(c.Request), c.Request.UserAgent()); err != nil {
		switch {
		case errors.Is(err, errShellEnded):
			closeWebSocket(ws, shellNotFoundCode, "terminal session not found")
		case started:
			// Nobody learnt this shell's id: end it rather than leave it
			// waiting for a page that cannot come back.
			if err := sh.close(0, ""); err != nil {
				log.Errorf("terminal session of %s: %s", account.Username, err)
			}
		}
		return
	}
	sh.readInput(ws)

	// A cancelled request means the session watcher revoked the sign-in
	// (sign-out, password change, account removed) or it expired: end the
	// shell rather than keep it for the next page.
	if c.Request.Context().Err() != nil {
		if err := sh.close(0, ""); err != nil {
			log.Errorf("terminal session of %s: %s", account.Username, err)
		}
		return
	}
	sh.detach(ws)
}

type terminalSessionInfo struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	// The command in the foreground: the shell itself at its prompt.
	Command   string `json:"command"`
	CreatedAt int64  `json:"createdAt"`
	Attached  bool   `json:"attached"`
	// Without a page: when it lost it, and when it ends unless one attaches.
	DetachedAt int64 `json:"detachedAt,omitempty"`
	EndsAt     int64 `json:"endsAt,omitempty"`
	// The page attached now, or last.
	ClientIP  string `json:"clientIp"`
	UserAgent string `json:"userAgent"`
	// The requesting user's own shell: only these can be opened.
	Own bool `json:"own"`
}

// GetTerminalSessions lists the running shells of all users.
func (s *Service) GetTerminalSessions(c *gin.Context) {
	var rsp proto.Response
	account := middleware.CurrentAccount(c)
	list := shells.list()
	infos := make([]terminalSessionInfo, 0, len(list))
	for _, sh := range list {
		info := terminalSessionInfo{
			ID:        sh.id,
			Username:  sh.account.Username,
			Command:   sh.foreground(),
			CreatedAt: sh.created.UnixMilli(),
			Own:       sh.account.Same(account),
		}
		sh.mutex.Lock()
		info.Attached = sh.ws != nil
		info.ClientIP, info.UserAgent = sh.clientIP, sh.userAgent
		if !info.Attached {
			info.DetachedAt = sh.detachedAt.UnixMilli()
			info.EndsAt = sh.detachedAt.Add(shellDetachedTimeout).UnixMilli()
		}
		sh.mutex.Unlock()
		infos = append(infos, info)
	}
	rsp.OkRspWithData(c, gin.H{"sessions": infos, "max": maxShells})
}

// CloseTerminalSession ends a shell, any user's, and its programs.
func (s *Service) CloseTerminalSession(c *gin.Context) {
	var rsp proto.Response
	sh := shells.get(c.Param("id"))
	if sh == nil {
		rsp.ErrRsp(c, -1, "terminal session not found")
		return
	}
	if err := sh.close(shellClosedCode, "terminal session closed"); err != nil {
		log.Errorf("terminal session of %s: %s", sh.account.Username, err)
		rsp.ErrRsp(c, -1, "programs of the terminal session are still running")
		return
	}
	rsp.OkRsp(c)
}
