package vm

import (
	"NanoKVM-Server/common"
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
	// Modes whose boot image is installed: fhd (1920x1080, 52 MiB), qhd
	// (2560x1440, 68 MiB) and each as a fixed carveout that Linux never
	// uses (2 MiB smaller: a CMA region is a multiple of 4 MiB), and uhd
	// (3840x2160, 118 MiB, fixed only).
	Modes          []string `json:"modes"`
	Available      bool     `json:"available"`
	RebootRequired bool     `json:"rebootRequired"`
}

// videoMemoryMode is the ION pool of one boot image. fhd and qhd are
// reusable CMA, whose idle pages stay available to Linux, unless fixed; uhd
// has no CMA image (see common.VideoPool).
type videoMemoryMode struct {
	name  string
	mib   int
	fixed bool
}

var videoMemoryModes = []videoMemoryMode{
	{"fhd", 52, false}, {"fhd-fixed", 50, true},
	{"qhd", 68, false}, {"qhd-fixed", 66, true},
	{"uhd", 118, true},
}

func videoMemoryPool(mode string) (common.VideoPool, bool) {
	for _, m := range videoMemoryModes {
		if m.name == mode {
			return common.VideoPool{MiB: m.mib, Reusable: !m.fixed}, true
		}
	}
	return common.VideoPool{}, false
}

func validVideoMemoryMode(mode string) bool {
	_, ok := videoMemoryPool(mode)
	return ok
}

// normalizeVideoMemoryMode accepts the names of earlier releases, which keep
// their capability: cma (128 MiB CMA) is qhd and fixed is qhd-fixed. legacy
// says that the name was one of them.
func normalizeVideoMemoryMode(mode string) (normalized string, legacy bool) {
	switch mode {
	case "cma":
		return "qhd", true
	case "fixed":
		return "qhd-fixed", true
	}
	if validVideoMemoryMode(mode) {
		return mode, false
	}
	return "", false
}

// videoMemoryImageName is the boot image of a board: the default fhd is the
// board's own image, the others add the mode (platform/build.sh).
func videoMemoryImageName(board, mode string) string {
	if mode == "fhd" {
		return board + ".dtb"
	}
	return board + "-" + mode + ".dtb"
}

// runningVideoMemoryMode is the mode of the booted image and whether it has
// an earlier release's name.
func runningVideoMemoryMode(root string) (mode string, legacy bool) {
	read := func(path string) string {
		b, _ := os.ReadFile(filepath.Join(root, path))
		return strings.Trim(string(b), "\x00 \r\n")
	}
	if mode, legacy = normalizeVideoMemoryMode(read("sys/firmware/devicetree/base/nanokvm,video-memory-mode")); mode != "" {
		return mode, legacy
	}
	// Boot images from before the property: CMA, or a fixed carveout of 128
	// MiB (uhd) or less.
	if _, err := os.Stat(filepath.Join(root, "sys/firmware/devicetree/base/cvitek-ion/heap-carveout/nanokvm,cma-backend")); err == nil {
		return "qhd", true
	}
	if read("sys/firmware/devicetree/base/reserved-memory/ion/compatible") == "ion-region" {
		if ionSizeMiB(root) >= 128 {
			return "uhd", false
		}
		return "qhd-fixed", true
	}
	return "unknown", false
}

func readVideoMemoryStatus(root string) videoMemoryStatus {
	read := func(path string) string {
		b, _ := os.ReadFile(filepath.Join(root, path))
		return strings.Trim(string(b), "\x00 \r\n")
	}
	active, runningLegacy := runningVideoMemoryMode(root)
	// The same rule as activate-kernel: the saved selection, else the mode of
	// the running image, so that an upgraded device never loses capability and
	// a new one gets fhd.
	file := read("etc/kvm/video-memory-mode")
	selected, _ := normalizeVideoMemoryMode(file)
	if selected == "" {
		selected = "fhd"
		if active != "unknown" {
			selected = active
		}
	}
	board := read("sys/firmware/devicetree/base/sipeed,board-revision")
	modes := []string{}
	switch board {
	case "alpha", "beta", "pcie", "lite":
		exists := func(name string) bool {
			_, err := os.Stat(filepath.Join(root, "usr/lib/nanokvm/boot", name))
			return err == nil
		}
		// The previous kernel package has board.dtb (128 MiB CMA), board-fixed
		// and board-uhd: board.dtb is not fhd there, so offer nothing until
		// the package is updated.
		if !exists(board+"-fixed.dtb") || exists(board+"-qhd.dtb") {
			for _, mode := range videoMemoryModes {
				// The kernel package composes boot.sd from a template and NAME.dtb.
				if exists(videoMemoryImageName(board, mode.name)) {
					modes = append(modes, mode.name)
				}
			}
		}
	}
	// The boot image of a running legacy name is replaced by activation, which
	// saves the new name: the next boot then uses a different pool.
	pendingImage := runningLegacy && file == selected
	return videoMemoryStatus{Active: active, Selected: selected, SizeMiB: ionSizeMiB(root), Modes: modes,
		Available: len(modes) > 1, RebootRequired: active != "unknown" && (active != selected || pendingImage)}
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
	// An earlier web interface may still send cma, fixed or uhd.
	if c.ShouldBindJSON(&req) != nil {
		req.Mode = ""
	}
	mode, _ := normalizeVideoMemoryMode(req.Mode)
	if mode == "" {
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
	if !slices.Contains(readVideoMemoryStatus("/").Modes, mode) {
		rsp.ErrRsp(c, -2, "Install the kernel package with this video memory mode first")
		return
	}
	// The monitor profile is programmed into the HDMI receiver and survives
	// the restart: refuse a mode that cannot capture it rather than boot into
	// "Current resolution is not supported".
	pool, _ := videoMemoryPool(mode)
	if err = common.MonitorFitsVideoPool(pool); err != nil {
		rsp.ErrRsp(c, -3, "Cannot select "+mode+": "+err.Error()+". Lower the monitor setting in the video settings first.")
		return
	}
	board, err := os.ReadFile("/sys/firmware/devicetree/base/sipeed,board-revision")
	if err != nil {
		rsp.ErrRsp(c, -2, "Board profile unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "/usr/libexec/nanokvm/activate-kernel", strings.Trim(string(board), "\x00 \r\n"), mode).CombinedOutput()
	if err != nil {
		if len(output) > 800 {
			output = output[len(output)-800:]
		}
		rsp.ErrRsp(c, -2, "Failed to select video memory mode: "+strings.TrimSpace(string(output)))
		return
	}
	s.GetMemoryStatus(c)
}
