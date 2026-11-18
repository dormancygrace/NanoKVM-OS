package vm

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"NanoKVM-Server/authn"
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
	// How often the sign-ins of the running shells are checked against the
	// account store, which can change without the server (reset button,
	// edited file).
	shellAccountRecheck = 5 * time.Second
	// How long the programs of an ending shell get after SIGHUP before
	// SIGKILL, and how long SIGKILL may take.
	shellHangupWait = 500 * time.Millisecond
	shellKillWait   = 2 * time.Second

	// WebSocket close codes the terminal page acts on.
	shellNotFoundCode  = 4404 // the requested shell has ended, or is another sign-in's
	shellTakenOverCode = 4409 // another page attached to this shell
	shellClosedCode    = 4410 // closed from the session list
	shellEndedCode     = 4411 // the shell exited, or nobody attached in time
	shellLimitCode     = 4429 // maxShells are running
)

type WinSize struct {
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
}

var (
	errShellLimit = errors.New("too many terminal sessions")
	errShellEnded = errors.New("terminal session ended")
)

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

// shell is one login shell on a pty, in its own session (setsid) with the
// jobs started from it. It belongs to the sign-in that started it and runs
// until it exits, is closed from the session list, that sign-in stops being
// valid, or it stays without a page for shellDetachedTimeout. At most one page
// is attached at a time.
type shell struct {
	id      string
	account middleware.Account
	created time.Time
	cmd     *exec.Cmd
	ptmx    *os.File

	// writeMutex keeps one writer on the attached WebSocket and orders the
	// replay before the output that follows it.
	writeMutex sync.Mutex

	mutex      sync.Mutex // guards the fields below
	ws         *websocket.Conn
	clientIP   string // of the page attached now, or last
	userAgent  string
	detachedAt time.Time
	// detached counts the detachments and attachments: a timeout belongs to
	// the detachment that started it and does nothing once another happened.
	detached uint64
	timer    *time.Timer
	replay   replayRing
	// Output bytes produced, and of those the ones a page received.
	produced  uint64
	delivered uint64
	closed    bool
}

type shellRegistry struct {
	mutex    sync.Mutex
	shells   map[string]*shell
	watching bool
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

// start runs a new login shell for account.
func (r *shellRegistry) start(account middleware.Account) (*shell, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if len(r.shells) >= maxShells {
		return nil, errShellLimit
	}

	cmd := terminalCommand()
	cmd.Dir = "/root"
	// Match the browser emulator; the installed rootfs includes this terminfo.
	cmd.Env = append(cmd.Environ(), "HOME=/root", "TERM=xterm-256color")
	// pty.Start makes the shell a session leader: its session id is its pid.
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

	s := &shell{id: newShellID(), account: account, created: time.Now(), cmd: cmd, ptmx: ptmx}
	r.shells[s.id] = s
	if !r.watching {
		r.watching = true
		go r.watch()
	}
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

// watch ends the shells whose sign-in the account store no longer accepts,
// attached or not, until no shell is left; start begins it again.
func (r *shellRegistry) watch() {
	ticker := time.NewTicker(shellAccountRecheck)
	defer ticker.Stop()
	for range ticker.C {
		valid := make(map[middleware.Account]bool)
		for _, s := range r.list() {
			ok, seen := valid[s.account]
			if !seen {
				ok = s.account.Valid(authn.RoleAdmin)
				valid[s.account] = ok
			}
			if !ok {
				if err := s.close(middleware.SessionRevokedCloseCode, "session expired or revoked"); err != nil {
					log.Errorf("terminal session of %s: %s", s.account.Username, err)
				}
			}
		}

		r.mutex.Lock()
		if len(r.shells) == 0 {
			r.watching = false
			r.mutex.Unlock()
			return
		}
		r.mutex.Unlock()
	}
}

// closeUser ends the shells of a user whose sessions were revoked through the
// server (sign-out, password change, account removed or changed).
func (r *shellRegistry) closeUser(username string) {
	for _, s := range r.list() {
		if s.account.Username == username {
			if err := s.close(middleware.SessionRevokedCloseCode, "session expired or revoked"); err != nil {
				log.Errorf("terminal session of %s: %s", username, err)
			}
		}
	}
}

// attach makes ws the shell's page: it tells the page the shell's id and how
// much output follows, sends the retained output, and closes the page
// attached before, if any. errShellEnded means the shell is gone; any other
// error is the page's connection, and leaves the shell detached.
func (s *shell) attach(ws *websocket.Conn, clientIP, userAgent string) error {
	s.writeMutex.Lock()
	defer s.writeMutex.Unlock()

	s.mutex.Lock()
	if s.closed {
		s.mutex.Unlock()
		return errShellEnded
	}
	previous := s.ws
	s.ws, s.clientIP, s.userAgent = ws, clientIP, userAgent
	s.detached++
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	replay := s.replay.bytes()
	// The end of the replay that no page received: output produced while
	// detached, whose terminal queries are still unanswered.
	pending := min(s.produced-s.delivered, uint64(len(replay)))
	s.mutex.Unlock()

	if previous != nil {
		closeWebSocket(previous, shellTakenOverCode, "terminal session opened elsewhere")
	}
	hello, _ := json.Marshal(map[string]any{"session": s.id, "replay": len(replay), "pending": pending})
	_ = ws.SetWriteDeadline(time.Now().Add(messageWait))
	err := ws.WriteMessage(websocket.TextMessage, hello)
	if err == nil && len(replay) > 0 {
		err = ws.WriteMessage(websocket.BinaryMessage, replay)
	}
	if err != nil {
		s.detach(ws)
		return err
	}
	s.mutex.Lock()
	s.delivered = s.produced
	s.mutex.Unlock()
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
	s.detached++
	detachment := s.detached
	s.timer = time.AfterFunc(shellDetachedTimeout, func() { s.expire(detachment) })
}

// expire ends the shell if it is still in the detachment that started the
// timeout: no page attached since, not even one that left again.
func (s *shell) expire(detachment uint64) {
	s.mutex.Lock()
	if s.closed || s.ws != nil || s.detached != detachment {
		s.mutex.Unlock()
		return
	}
	ws := s.markClosed()
	s.mutex.Unlock()
	if err := s.finish(ws, shellEndedCode, "terminal session ended"); err != nil {
		log.Errorf("terminal session of %s: %s", s.account.Username, err)
	}
}

// readOutput copies the pty to the replay ring and the attached page until
// nothing holds the terminal open any more.
func (s *shell) readOutput() {
	data := make([]byte, maxMessageSize)
	for {
		n, err := s.ptmx.Read(data)
		if n > 0 {
			s.output(data[:n])
		}
		if err != nil {
			if err := s.close(shellEndedCode, "terminal session ended"); err != nil {
				log.Errorf("terminal session of %s: %s", s.account.Username, err)
			}
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
	s.produced += uint64(len(data))
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
		return
	}
	s.mutex.Lock()
	if s.ws == ws {
		s.delivered = s.produced
	}
	s.mutex.Unlock()
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

// close ends the shell and every program of its session. A non-zero code
// tells the attached page why. The error says programs are still running.
func (s *shell) close(code int, reason string) error {
	s.mutex.Lock()
	if s.closed {
		s.mutex.Unlock()
		return nil
	}
	ws := s.markClosed()
	s.mutex.Unlock()
	return s.finish(ws, code, reason)
}

// markClosed marks the shell closed and takes its page; s.mutex is held.
func (s *shell) markClosed() *websocket.Conn {
	s.closed = true
	ws := s.ws
	s.ws = nil
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	return ws
}

func (s *shell) finish(ws *websocket.Conn, code int, reason string) error {
	shells.remove(s.id)
	if ws != nil {
		if code != 0 {
			closeWebSocket(ws, code, reason)
		} else {
			_ = ws.Close()
		}
	}
	err := endSession(s.cmd.Process.Pid)
	_ = s.ptmx.Close()
	_ = s.cmd.Process.Kill()
	_ = s.cmd.Wait()
	log.Debugf("terminal session of %s ended", s.account.Username)
	return err
}

// endSession ends every process of session sid: the shell, its foreground
// program and its background jobs, each in its own process group. They get
// SIGHUP, as from a hung-up terminal, then SIGKILL. A program that left the
// session on purpose (setsid, a service it started) keeps running.
func endSession(sid int) error {
	signal := func(sig syscall.Signal) int {
		pids := sessionProcesses(sid)
		for _, pid := range pids {
			_ = syscall.Kill(pid, sig)
		}
		return len(pids)
	}
	if signal(syscall.SIGHUP) == 0 {
		return nil
	}
	signal(syscall.SIGCONT)
	for deadline := time.Now().Add(shellHangupWait); time.Now().Before(deadline); {
		time.Sleep(50 * time.Millisecond)
		if len(sessionProcesses(sid)) == 0 {
			return nil
		}
	}
	for deadline := time.Now().Add(shellKillWait); time.Now().Before(deadline); {
		if signal(syscall.SIGKILL) == 0 {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("programs of the terminal session still running: %v", sessionProcesses(sid))
}

// sessionProcesses lists the live (not zombie) processes of session sid.
func sessionProcesses(sid int) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var pids []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if err != nil {
			continue
		}
		// pid (comm) state ppid pgrp session ...; comm may contain anything.
		i := strings.LastIndexByte(string(stat), ')')
		if i < 0 {
			continue
		}
		fields := strings.Fields(string(stat[i+1:]))
		if len(fields) < 4 || fields[0] == "Z" || fields[0] == "X" {
			continue
		}
		if session, err := strconv.Atoi(fields[3]); err == nil && session == sid {
			pids = append(pids, pid)
		}
	}
	return pids
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
