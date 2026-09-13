package vm

import (
	"encoding/json"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

const (
	messageWait      = 10 * time.Second
	maxMessageSize   = 1024
	terminalPongWait = 30 * time.Second
)

type WinSize struct {
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
}

func runTerminalSession(ws *websocket.Conn, cmd *exec.Cmd) error {
	// Match the browser emulator; the installed rootfs includes this terminfo.
	cmd.Env = append(cmd.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		return err
	}
	defer func() {
		_ = ptmx.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	// creack/pty requires nonblocking mode for Close to interrupt a Read.
	if err := syscall.SetNonblock(int(ptmx.Fd()), true); err != nil {
		return err
	}
	// Detect a vanished browser/network even when no terminal data is flowing.
	_ = ws.SetReadDeadline(time.Now().Add(terminalPongWait))
	ws.SetPongHandler(func(string) error { return ws.SetReadDeadline(time.Now().Add(terminalPongWait)) })
	stopPing := make(chan struct{})
	defer close(stopPing)
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopPing:
				return
			case <-ticker.C:
				if ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(messageWait)) != nil {
					_ = ws.Close()
					return
				}
			}
		}
	}()
	done := make(chan struct{})
	go func() {
		defer close(done)
		// EOF from an exited shell or failed output must unblock wsRead too.
		defer ws.Close()
		wsWrite(ws, ptmx)
	}()
	wsRead(ws, ptmx)
	_ = ws.Close()
	_ = ptmx.Close()
	<-done
	return nil
}

func wsWrite(ws *websocket.Conn, ptmx *os.File) {
	data := make([]byte, maxMessageSize)
	for {
		n, err := ptmx.Read(data)
		if n > 0 {
			_ = ws.SetWriteDeadline(time.Now().Add(messageWait))
			if ws.WriteMessage(websocket.BinaryMessage, data[:n]) != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func wsRead(ws *websocket.Conn, ptmx *os.File) {
	for {
		kind, data, err := ws.ReadMessage()
		if err != nil {
			return
		}
		if kind == websocket.BinaryMessage {
			var size WinSize
			if json.Unmarshal(data, &size) == nil && size.Rows > 0 && size.Cols > 0 {
				_ = pty.Setsize(ptmx, &pty.Winsize{Rows: size.Rows, Cols: size.Cols})
			}
			continue
		}
		if _, err = ptmx.Write(data); err != nil {
			return
		}
	}
}
