package vm

import (
	"NanoKVM-Server/osupdate"
	"NanoKVM-Server/proto"
	"context"
	"github.com/gin-gonic/gin"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type videoMemoryStatus struct {
	Active   string `json:"active"`
	Selected string `json:"selected"`
	SizeMiB  int    `json:"sizeMiB"`
	// Modes whose boot image is installed: cma (128 MiB lent to Linux),
	// fixed (64 MiB) and uhd (128 MiB fixed, needed for 3840x2160).
	Modes          []string `json:"modes"`
	Available      bool     `json:"available"`
	RebootRequired bool     `json:"rebootRequired"`
}

var videoMemoryModes = []string{"cma", "fixed", "uhd"}

func validVideoMemoryMode(mode string) bool { return mode == "cma" || mode == "fixed" || mode == "uhd" }
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
			if ionSizeMiB(root) >= 128 {
				active = "uhd"
			}
		} else {
			active = "unknown"
		}
	}
	selected := read("etc/kvm/video-memory-mode")
	if !validVideoMemoryMode(selected) {
		selected = "cma"
	}
	board := read("sys/firmware/devicetree/base/sipeed,board-revision")
	modes := []string{}
	switch board {
	case "alpha", "beta", "pcie", "lite":
		for _, mode := range videoMemoryModes {
			// The kernel package composes boot.sd from a template and NAME.dtb.
			name := board + ".dtb"
			if mode != "cma" {
				name = board + "-" + mode + ".dtb"
			}
			if _, err := os.Stat(filepath.Join(root, "usr/lib/nanokvm/boot", name)); err == nil {
				modes = append(modes, mode)
			}
		}
	}
	return videoMemoryStatus{Active: active, Selected: selected, SizeMiB: ionSizeMiB(root), Modes: modes,
		Available: len(modes) > 1, RebootRequired: active != "unknown" && active != selected}
}

func ionSizeMiB(root string) int {
	data, err := os.ReadFile(filepath.Join(root, "sys/firmware/devicetree/base/reserved-memory/ion/size"))
	if err != nil || len(data) != 4 {
		return 0
	}
	return int(uint32(data[0])<<24|uint32(data[1])<<16|uint32(data[2])<<8|uint32(data[3])) >> 20
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
	if !slices.Contains(readVideoMemoryStatus("/").Modes, req.Mode) {
		rsp.ErrRsp(c, -2, "Install the kernel package with this video memory mode first")
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
