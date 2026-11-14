package vm

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/internal/oomscore"
	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/extensions/apkpkg"
)

const ScriptDirectory = "/etc/kvm/scripts"

// maxScriptUpload bounds multipart parsing, which otherwise spills
// arbitrarily large uploads into the 64 MiB /tmp tmpfs.
const maxScriptUpload = 16 << 20

var (
	errInvalidScript        = errors.New("invalid script")
	errPythonRuntimeMissing = errors.New("Python runtime is not installed")
	pythonInterpreter       = "/usr/bin/python3"
)

func scriptRoot(root string) error {
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errInvalidScript
	}
	return nil
}

func validScriptName(name string, nested bool) bool {
	if name == "" || filepath.IsAbs(name) || filepath.Clean(name) != name || !isScript(name) {
		return false
	}
	if strings.ContainsRune(name, '\\') || (!nested && strings.ContainsRune(name, '/')) {
		return false
	}
	for _, value := range name {
		if value < 0x20 || value == 0x7f {
			return false
		}
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// scriptPath resolves only a regular file below root. Symlinks are rejected at
// every component so execution and deletion cannot escape through an alias.
func scriptPath(root, name string) (string, error) {
	if !validScriptName(name, true) {
		return "", errInvalidScript
	}
	if err := scriptRoot(root); err != nil {
		return "", err
	}

	current := root
	parts := strings.Split(name, "/")
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errInvalidScript
		}
		if index < len(parts)-1 && !info.IsDir() {
			return "", errInvalidScript
		}
		if index == len(parts)-1 && !info.Mode().IsRegular() {
			return "", errInvalidScript
		}
	}
	return current, nil
}

func listScripts(root string) ([]string, error) {
	if err := scriptRoot(root); err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	files := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if isScript(relative) {
			files = append(files, relative)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func saveScript(root, name string, source io.Reader) (string, error) {
	if !validScriptName(name, false) {
		return "", errInvalidScript
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	if err := scriptRoot(root); err != nil {
		return "", err
	}

	temporary, err := os.CreateTemp(root, ".script-upload-*")
	if err != nil {
		return "", err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o700); err != nil {
		temporary.Close()
		return "", err
	}
	if _, err := io.Copy(temporary, source); err != nil {
		temporary.Close()
		return "", err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	target := filepath.Join(root, name)
	if err := os.Rename(temporaryName, target); err != nil {
		return "", err
	}
	return target, nil
}

func scriptCommand(path string) (*exec.Cmd, error) {
	if strings.EqualFold(filepath.Ext(path), ".py") {
		if _, err := exec.LookPath(pythonInterpreter); err != nil {
			return nil, errPythonRuntimeMissing
		}
		return exec.Command(pythonInterpreter, path), nil
	}
	return exec.Command(path), nil
}

func executeScript(path, runType string) ([]byte, error) {
	cmd, err := scriptCommand(path)
	if err != nil {
		return nil, err
	}
	program, err := exec.LookPath(cmd.Path)
	if err != nil {
		return nil, err
	}
	argv := oomscore.Unprotected(program, cmd.Args[1:]...)
	cmd = exec.Command(argv[0], argv[1:]...)
	switch runType {
	case "foreground":
		return runForeground(cmd, scriptRunLimit)
	case "background":
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		go func() {
			if err := cmd.Wait(); err != nil {
				log.Errorf("background script %q failed: %s", path, err)
			}
		}()
		return nil, nil
	default:
		return nil, errInvalidScript
	}
}

// "Run" in the Scripts window waits for the script: at most scriptRunLimit,
// so that the answer comes before the browser stops waiting (60 s), with the
// first scriptOutputLimit bytes of its output, as a startup run keeps.
const (
	scriptRunLimit    = 50 * time.Second
	scriptOutputLimit = autostartLogLimit
)

// runForeground runs cmd and returns its output, standard output and errors
// together. Output past the limit is read and dropped, so a talkative script
// is never blocked. At the time limit the script and what it started (its
// process group) are killed. The output ends with a note when it was cut,
// the script was stopped, or it exited with a code other than 0.
func runForeground(cmd *exec.Cmd, limit time.Duration) ([]byte, error) {
	out := &cappedOutput{limit: scriptOutputLimit}
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// A process it left in the background may hold the output open.
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	var stopped atomic.Bool
	timer := time.AfterFunc(limit, func() {
		stopped.Store(true)
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	})
	err := cmd.Wait()
	timer.Stop()
	output := out.bytes()
	if out.cut {
		output = fmt.Appendf(output, "\n[output cut after %d KiB]", scriptOutputLimit>>10)
	}
	var exit *exec.ExitError
	switch {
	case stopped.Load():
		output = fmt.Appendf(output, "\n[stopped after %d s; use Run in background for longer scripts]", int(limit/time.Second))
	case errors.As(err, &exit):
		output = fmt.Appendf(output, "\n[exit code %d]", exit.ExitCode())
	case err != nil && !errors.Is(err, exec.ErrWaitDelay):
		return output, err
	}
	return output, nil
}

// cappedOutput keeps the first limit bytes written to it.
type cappedOutput struct {
	mu    sync.Mutex
	limit int
	data  []byte
	cut   bool
}

func (c *cappedOutput) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// All of it is reported written: a short write would stop the copy and
	// leave the script blocked on a full pipe.
	n := len(p)
	if room := c.limit - len(c.data); n > room {
		c.cut = true
		p = p[:max(room, 0)]
	}
	c.data = append(c.data, p...)
	return n, nil
}

func (c *cappedOutput) bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.data
}

// codePythonMissing answers a run of a .py script without Python 3: the
// page offers to install it.
const codePythonMissing = -3

func pythonInstalled() bool {
	_, err := exec.LookPath(pythonInterpreter)
	return err == nil
}

// InstallPython installs Python 3 from the Alpine repositories (about 13 MiB
// to download, 35 MiB on the device), for .py scripts. It is not part of
// the OS: most devices never run one.
func (s *Service) InstallPython(c *gin.Context) {
	var rsp proto.Response
	if !pythonInstalled() {
		if err := apkpkg.Run("install", "python3"); err != nil {
			rsp.ErrRsp(c, -1, "Python installation failed: "+err.Error())
			return
		}
	}
	rsp.OkRsp(c)
}

func removeScript(root, name string) error {
	path, err := scriptPath(root, name)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

func (s *Service) GetScripts(c *gin.Context) {
	var rsp proto.Response

	files, err := listScripts(ScriptDirectory)
	if err != nil {
		rsp.ErrRsp(c, -1, "get scripts failed")
		return
	}

	autostart, err := readAutostart()
	if err != nil {
		log.Errorf("read autostart list: %s", err)
		autostart = []string{}
	}
	rsp.OkRspWithData(c, &proto.GetScriptsRsp{
		Files:           files,
		Autostart:       autostart,
		AutostartStatus: autostartStatus(autostart),
	})

	log.Debugf("get scripts total %d", len(files))
}

func (s *Service) UploadScript(c *gin.Context) {
	var rsp proto.Response

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxScriptUpload)
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		rsp.ErrRsp(c, -1, "bad request")
		return
	}
	defer file.Close()

	if !validScriptName(header.Filename, false) {
		rsp.ErrRsp(c, -2, "invalid arguments")
		return
	}

	if _, err = saveScript(ScriptDirectory, header.Filename, file); err != nil {
		rsp.ErrRsp(c, -2, "save failed")
		return
	}

	data := &proto.UploadScriptRsp{
		File:          header.Filename,
		PythonMissing: strings.EqualFold(filepath.Ext(header.Filename), ".py") && !pythonInstalled(),
	}
	rsp.OkRspWithData(c, data)

	log.Debugf("upload script %q success", header.Filename)
}

func (s *Service) RunScript(c *gin.Context) {
	var req proto.RunScriptReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}

	path, err := scriptPath(ScriptDirectory, req.Name)
	if err != nil || req.Type != "foreground" && req.Type != "background" {
		rsp.ErrRsp(c, -2, "invalid arguments")
		return
	}

	output, err := executeScript(path, req.Type)

	if err != nil {
		log.Errorf("run script %q failed: %s", req.Name, err.Error())
		if errors.Is(err, errPythonRuntimeMissing) {
			rsp.ErrRsp(c, codePythonMissing, errPythonRuntimeMissing.Error())
			return
		}
		rsp.ErrRsp(c, -2, "run script failed")
		return
	}

	rsp.OkRspWithData(c, &proto.RunScriptRsp{
		Log: string(output),
	})

	log.Debugf("run script %q success", req.Name)
}

func (s *Service) DeleteScript(c *gin.Context) {
	var req proto.DeleteScriptReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}

	if err := removeScript(ScriptDirectory, req.Name); err != nil {
		log.Errorf("delete script %q failed: %s", req.Name, err)
		rsp.ErrRsp(c, -3, "delete failed")
		return
	}
	if err := setAutostart(ScriptDirectory, req.Name, false); err != nil {
		log.Errorf("unmark deleted script %q: %s", req.Name, err)
	}

	rsp.OkRsp(c)
	log.Debugf("delete script %q success", req.Name)
}

// SetScriptAutostart marks or unmarks a script to run at startup.
func (s *Service) SetScriptAutostart(c *gin.Context) {
	var req proto.SetScriptAutostartReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}
	if err := setAutostart(ScriptDirectory, req.Name, req.Enabled); err != nil {
		log.Errorf("autostart %q: %s", req.Name, err)
		rsp.ErrRsp(c, -2, "invalid script")
		return
	}
	rsp.OkRsp(c)
	log.Debugf("autostart %q: %t", req.Name, req.Enabled)
}

func isScript(name string) bool {
	nameLower := strings.ToLower(name)
	if strings.HasSuffix(nameLower, ".sh") || strings.HasSuffix(nameLower, ".py") {
		return true
	}

	return false
}
