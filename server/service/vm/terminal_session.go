package vm

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"NanoKVM-Server/internal/oomscore"
	"NanoKVM-Server/middleware"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	messageWait = 10 * time.Second
	// maxReadSize bounds one complete client message, including fragmented
	// pastes, before the terminal handler receives it.
	maxReadSize      = 64 * 1024
	maxMessageSize   = 1024
	terminalPongWait = 30 * time.Second

	// A shell outlives its page. Without a page it waits this long for one to
	// attach again (a reload, a dropped network, another browser), then ends.
	shellDetachedTimeout = 10 * time.Minute
	// The most recent output a page receives when it attaches.
	shellReplaySize = 64 * 1024
	// Shells running at once, with or without a page.
	maxShells = 4

	// WebSocket close codes the terminal page acts on.
	shellNotFoundCode  = 4404 // the requested shell has ended, or is another user's
	shellTakenOverCode = 4409 // another page attached to this shell
	shellClosedCode    = 4410 // closed from the session list
	shellEndedCode     = 4411 // the shell exited, or nobody attached in time
	shellLimitCode     = 4429 // maxShells are running
)

type WinSize struct {
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
}

var errShellLimit = errors.New("too many terminal sessions")

// replayRing keeps the last shellReplaySize bytes of a shell's output.
type replayRing struct {
	buf   [shellReplaySize]byte
	start int
	size  int
}

func (r *replayRing) write(data []byte) {
	if len(data) >= len(r.buf) {
		copy(r.buf[:], data[len(data)-len(r.buf):])
		r.start, r.size = 0, len(r.buf)
		return
	}
	end := (r.start + r.size) % len(r.buf)
	n := copy(r.buf[end:], data)
	copy(r.buf[:], data[n:])
	r.size += len(data)
	if r.size > len(r.buf) {
		r.start = (r.start + r.size - len(r.buf)) % len(r.buf)
		r.size = len(r.buf)
	}
}

func (r *replayRing) bytes() []byte {
	out := make([]byte, r.size)
	n := copy(out, r.buf[r.start:min(r.start+r.size, len(r.buf))])
	copy(out[n:], r.buf[:r.size-n])
	return out
}

// shell is one login shell on a pty. It belongs to the user who started it and
// runs until it exits, is closed from the session list, the user's sessions
// are revoked, or it stays without a page for shellDetachedTimeout. At most
// one page is attached at a time.
type shell struct {
	id       string
	username string
	created  time.Time
	cmd      *exec.Cmd
	ptmx     *os.File

	// writeMutex keeps one writer on the attached WebSocket and orders the
	// replay before the output that follows it.
	writeMutex sync.Mutex

	mutex      sync.Mutex // guards the fields below
	ws         *websocket.Conn
	clientIP   string // of the page attached now, or last
	userAgent  string
	detachedAt time.Time
	timer      *time.Timer
	replay     replayRing
	closed     bool
}

type shellRegistry struct {
	mutex  sync.Mutex
	shells map[string]*shell
}

var shells = &shellRegistry{shells: make(map[string]*shell)}

func init() {
	middleware.OnUserRevoked(shells.closeUser)
}

func newShellID() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

// start runs a new login shell for username.
func (r *shellRegistry) start(username string) (*shell, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if len(r.shells) >= maxShells {
		return nil, errShellLimit
	}

	cmd := terminalCommand()
	cmd.Dir = "/root"
	// Match the browser emulator; the installed rootfs includes this terminfo.
	cmd.Env = append(cmd.Environ(), "HOME=/root", "TERM=xterm-256color")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		return nil, err
	}
	// Commands typed here, apk included, must not share the server's OOM
	// protection.
	_ = oomscore.Set(cmd.Process.Pid, oomscore.Normal)
	// creack/pty requires nonblocking mode for Close to interrupt a Read.
	if err := syscall.SetNonblock(int(ptmx.Fd()), true); err != nil {
		_ = ptmx.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}

	s := &shell{id: newShellID(), username: username, created: time.Now(), cmd: cmd, ptmx: ptmx}
	r.shells[s.id] = s
	go s.readOutput()
	return s, nil
}

func (r *shellRegistry) get(id string) *shell {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.shells[id]
}

func (r *shellRegistry) remove(id string) {
	r.mutex.Lock()
	delete(r.shells, id)
	r.mutex.Unlock()
}

func (r *shellRegistry) list() []*shell {
	r.mutex.Lock()
	out := make([]*shell, 0, len(r.shells))
	for _, s := range r.shells {
		out = append(out, s)
	}
	r.mutex.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].created.Before(out[j].created) })
	return out
}

// closeUser ends the shells of a user whose sessions were revoked (sign-out,
// password change, account removed or changed), detached ones included.
func (r *shellRegistry) closeUser(username string) {
	for _, s := range r.list() {
		if s.username == username {
			s.close(0, "")
		}
	}
}

// attach makes ws the shell's page: it tells the page the shell's id, sends
// the retained output, and closes the page attached before, if any.
func (s *shell) attach(ws *websocket.Conn, clientIP, userAgent string) error {
	s.writeMutex.Lock()
	defer s.writeMutex.Unlock()

	s.mutex.Lock()
	if s.closed {
		s.mutex.Unlock()
		return errors.New("terminal session ended")
	}
	previous := s.ws
	s.ws, s.clientIP, s.userAgent = ws, clientIP, userAgent
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	replay := s.replay.bytes()
	s.mutex.Unlock()

	if previous != nil {
		closeWebSocket(previous, shellTakenOverCode, "terminal session opened elsewhere")
	}
	// The page writes the replay without answering the terminal queries in it
	// (cursor position and the like): they were answered when they ran.
	hello, _ := json.Marshal(map[string]any{"session": s.id, "replay": len(replay)})
	_ = ws.SetWriteDeadline(time.Now().Add(messageWait))
	if err := ws.WriteMessage(websocket.TextMessage, hello); err != nil {
		return err
	}
	if len(replay) > 0 {
		return ws.WriteMessage(websocket.BinaryMessage, replay)
	}
	return nil
}

// detach leaves the shell without a page and starts its timeout. A page that
// another one replaced is not attached any more and changes nothing.
func (s *shell) detach(ws *websocket.Conn) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.closed || s.ws != ws {
		return
	}
	s.ws = nil
	s.detachedAt = time.Now()
	s.timer = time.AfterFunc(shellDetachedTimeout, func() {
		s.mutex.Lock()
		expired := s.ws == nil
		s.mutex.Unlock()
		if expired {
			s.close(shellEndedCode, "terminal session ended")
		}
	})
}

// readOutput copies the pty to the replay ring and the attached page until
// the shell ends.
func (s *shell) readOutput() {
	data := make([]byte, maxMessageSize)
	for {
		n, err := s.ptmx.Read(data)
		if n > 0 {
			s.output(data[:n])
		}
		if err != nil {
			s.close(shellEndedCode, "terminal session ended")
			return
		}
	}
}

func (s *shell) output(data []byte) {
	s.writeMutex.Lock()
	defer s.writeMutex.Unlock()

	s.mutex.Lock()
	if s.closed {
		s.mutex.Unlock()
		return
	}
	s.replay.write(data)
	ws := s.ws
	s.mutex.Unlock()

	if ws == nil {
		return
	}
	_ = ws.SetWriteDeadline(time.Now().Add(messageWait))
	if ws.WriteMessage(websocket.BinaryMessage, data) != nil {
		// The page is gone; its reader ends too and the shell waits detached.
		_ = ws.Close()
		s.detach(ws)
	}
}

// readInput copies the page's keystrokes to the pty until the page goes away.
// Binary messages carry the window size.
func (s *shell) readInput(ws *websocket.Conn) {
	ws.SetReadLimit(maxReadSize)
	// Detect a vanished browser or network even when no terminal data flows.
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

	for {
		kind, data, err := ws.ReadMessage()
		if err != nil {
			return
		}
		if kind == websocket.BinaryMessage {
			var size WinSize
			if json.Unmarshal(data, &size) == nil && size.Rows > 0 && size.Cols > 0 {
				_ = pty.Setsize(s.ptmx, &pty.Winsize{Rows: size.Rows, Cols: size.Cols})
			}
			continue
		}
		if _, err = s.ptmx.Write(data); err != nil {
			return
		}
	}
}

// close ends the shell. A non-zero code tells the attached page why.
func (s *shell) close(code int, reason string) {
	s.mutex.Lock()
	if s.closed {
		s.mutex.Unlock()
		return
	}
	s.closed = true
	ws := s.ws
	s.ws = nil
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.mutex.Unlock()

	shells.remove(s.id)
	if ws != nil {
		if code != 0 {
			closeWebSocket(ws, code, reason)
		} else {
			_ = ws.Close()
		}
	}
	_ = s.ptmx.Close()
	_ = s.cmd.Process.Kill()
	_ = s.cmd.Wait()
	log.Debugf("terminal session of %s ended", s.username)
}

// foreground is the command running in the foreground of the shell's
// terminal: the shell itself at its prompt.
func (s *shell) foreground() string {
	raw, err := s.ptmx.SyscallConn()
	if err != nil {
		return ""
	}
	pgrp := 0
	_ = raw.Control(func(fd uintptr) {
		pgrp, err = unix.IoctlGetInt(int(fd), unix.TIOCGPGRP)
	})
	if err != nil || pgrp <= 0 {
		return ""
	}
	comm, err := os.ReadFile("/proc/" + strconv.Itoa(pgrp) + "/comm")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(comm))
}

func closeWebSocket(ws *websocket.Conn, code int, reason string) {
	_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason),
		time.Now().Add(messageWait))
	_ = ws.Close()
}
