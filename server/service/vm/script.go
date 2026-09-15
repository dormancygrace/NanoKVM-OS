package vm

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/proto"
)

const ScriptDirectory = "/etc/kvm/scripts"

var (
	errInvalidScript        = errors.New("invalid script")
	errPythonRuntimeMissing = errors.New("Python runtime is not installed")
	pythonInterpreter       = "/opt/nkos/addons/python/bin/python3"
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
	switch runType {
	case "foreground":
		return cmd.CombinedOutput()
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

	rsp.OkRspWithData(c, &proto.GetScriptsRsp{
		Files: files,
	})

	log.Debugf("get scripts total %d", len(files))
}

func (s *Service) UploadScript(c *gin.Context) {
	var rsp proto.Response

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
		File: header.Filename,
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
			rsp.ErrRsp(c, -2, errPythonRuntimeMissing.Error())
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

	rsp.OkRsp(c)
	log.Debugf("delete script %q success", req.Name)
}

func isScript(name string) bool {
	nameLower := strings.ToLower(name)
	if strings.HasSuffix(nameLower, ".sh") || strings.HasSuffix(nameLower, ".py") {
		return true
	}

	return false
}
