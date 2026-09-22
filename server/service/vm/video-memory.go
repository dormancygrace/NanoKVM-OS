package vm

import (
	"NanoKVM-Server/osupdate"
	"NanoKVM-Server/proto"
	"context"
	"github.com/gin-gonic/gin"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type videoMemoryStatus struct {
	Active         string `json:"active"`
	Selected       string `json:"selected"`
	SizeMiB        int    `json:"sizeMiB"`
	Available      bool   `json:"available"`
	RebootRequired bool   `json:"rebootRequired"`
}

func validVideoMemoryMode(mode string) bool { return mode == "cma" || mode == "fixed" }
func readVideoMemoryStatus(root string) videoMemoryStatus {
	read := func(path string) string {
		b, _ := os.ReadFile(filepath.Join(root, path))
		return strings.Trim(string(b), "\x00 \r\n")
	}
	active := read("sys/firmware/devicetree/base/nanokvm,video-memory-mode")
	if !validVideoMemoryMode(active) {
		if _, err := os.Stat(filepath.Join(root, "sys/firmware/devicetree/base/cvitek-ion/heap-carveout/nanokvm,cma-backend")); err == nil {
			active = "cma"
		} else if read("sys/firmware/devicetree/base/reserved-memory/ion/compatible") == "ion-region" {
			active = "fixed"
		} else {
			active = "unknown"
		}
	}
	selected := read("etc/kvm/video-memory-mode")
	if !validVideoMemoryMode(selected) {
		selected = "cma"
	}
	board := read("sys/firmware/devicetree/base/sipeed,board-revision")
	available := false
	switch board {
	case "alpha", "beta", "pcie", "lite":
		_, a := os.Stat(filepath.Join(root, "usr/lib/nanokvm/boot", board+".sd"))
		_, b := os.Stat(filepath.Join(root, "usr/lib/nanokvm/boot", board+"-fixed.sd"))
		available = a == nil && b == nil
	}
	return videoMemoryStatus{Active: active, Selected: selected, SizeMiB: 64, Available: available, RebootRequired: active != "unknown" && active != selected}
}
func (s *Service) SetVideoMemory(c *gin.Context) {
	var rsp proto.Response
	var req struct {
		Mode string `json:"mode"`
	}
	if c.ShouldBindJSON(&req) != nil || !validVideoMemoryMode(req.Mode) {
		rsp.ErrRsp(c, -1, "Invalid video memory mode")
		return
	}
	lock, err := osupdate.Lock()
	if err != nil {
		rsp.ErrRsp(c, -2, err.Error())
		return
	}
	defer lock.Close()
	memoryMutation.Lock()
	defer memoryMutation.Unlock()
	if !readVideoMemoryStatus("/").Available {
		rsp.ErrRsp(c, -2, "Install the kernel package with both video memory modes first")
		return
	}
	board, err := os.ReadFile("/sys/firmware/devicetree/base/sipeed,board-revision")
	if err != nil {
		rsp.ErrRsp(c, -2, "Board profile unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "/usr/libexec/nanokvm/activate-kernel", strings.Trim(string(board), "\x00 \r\n"), req.Mode).CombinedOutput()
	if err != nil {
		if len(output) > 800 {
			output = output[len(output)-800:]
		}
		rsp.ErrRsp(c, -2, "Failed to select video memory mode: "+strings.TrimSpace(string(output)))
		return
	}
	s.GetMemoryStatus(c)
}
