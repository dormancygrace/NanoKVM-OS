package vm

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"NanoKVM-Server/proto"
	"github.com/gin-gonic/gin"
)

const memoryService = "/etc/init.d/S38memory"

var memoryMutation sync.Mutex

type memorySwap struct {
	Enabled             bool   `json:"enabled"`
	Available           bool   `json:"available"`
	SizeMiB             int64  `json:"sizeMiB"`
	UsedBytes           uint64 `json:"usedBytes"`
	Priority            int64  `json:"priority"`
	MemoryBytes         uint64 `json:"memoryBytes,omitempty"`
	CompressedBytes     uint64 `json:"compressedBytes,omitempty"`
	OriginalBytes       uint64 `json:"originalBytes,omitempty"`
	Algorithm           string `json:"algorithm,omitempty"`
	Recompress          bool   `json:"recompress"`
	RecompressAvailable bool   `json:"recompressAvailable"`
	RecompressReady     bool   `json:"recompressReady"`
}
type memoryStatus struct {
	TotalBytes     uint64     `json:"totalBytes"`
	AvailableBytes uint64     `json:"availableBytes"`
	UsedBytes      uint64     `json:"usedBytes"`
	CachedBytes    uint64     `json:"cachedBytes"`
	SwapTotalBytes uint64     `json:"swapTotalBytes"`
	SwapUsedBytes  uint64     `json:"swapUsedBytes"`
	VideoBytes     uint64     `json:"videoBytes"`
	Zram           memorySwap `json:"zram"`
	SD             memorySwap `json:"sd"`
}
type memorySwapRequest struct {
	Kind       string `json:"kind"`
	Enabled    bool   `json:"enabled"`
	SizeMiB    int64  `json:"sizeMiB"`
	Recompress *bool  `json:"recompress,omitempty"`
}

func parseMemoryCounters(text string) map[string]uint64 {
	result := make(map[string]uint64)
	scan := bufio.NewScanner(strings.NewReader(text))
	for scan.Scan() {
		fields := strings.Fields(scan.Text())
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err == nil {
			result[strings.TrimSuffix(fields[0], ":")] = value * 1024
		}
	}
	return result
}

func parseActiveSwaps(text string) map[string]memorySwap {
	result := make(map[string]memorySwap)
	scan := bufio.NewScanner(strings.NewReader(text))
	for scan.Scan() {
		fields := strings.Fields(scan.Text())
		if len(fields) < 5 || fields[0] == "Filename" {
			continue
		}
		size, e1 := strconv.ParseInt(fields[2], 10, 64)
		used, e2 := strconv.ParseUint(fields[3], 10, 64)
		priority, e3 := strconv.ParseInt(fields[4], 10, 64)
		if e1 != nil || e2 != nil || e3 != nil || size <= 0 {
			continue
		}
		result[fields[0]] = memorySwap{Enabled: true, Available: true,
			SizeMiB: (size + 1023) / 1024, UsedBytes: used * 1024, Priority: priority}
	}
	return result
}

func validSwapRequest(req memorySwapRequest) bool {
	switch req.Kind {
	case "zram":
		return req.SizeMiB == 32 || req.SizeMiB == 64 || req.SizeMiB == 128 || req.SizeMiB == 162
	case "sd":
		if req.Recompress != nil {
			return false
		}
		return req.SizeMiB == 128 || req.SizeMiB == 256 || req.SizeMiB == 512
	}
	return false
}

func readMemoryStatus() (memoryStatus, error) {
	var result memoryStatus
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return result, err
	}
	counters := parseMemoryCounters(string(raw))
	result.TotalBytes = counters["MemTotal"]
	if result.TotalBytes == 0 {
		return result, fmt.Errorf("memory statistics unavailable")
	}
	result.AvailableBytes = min(counters["MemAvailable"], result.TotalBytes)
	result.UsedBytes = result.TotalBytes - result.AvailableBytes
	cached := counters["Cached"] + counters["Buffers"] + counters["SReclaimable"]
	result.CachedBytes = cached - min(cached, counters["Shmem"])
	result.SwapTotalBytes = counters["SwapTotal"]
	result.SwapUsedBytes = result.SwapTotalBytes - min(result.SwapTotalBytes, counters["SwapFree"])
	raw, err = os.ReadFile("/proc/swaps")
	if err != nil {
		return result, err
	}
	active := parseActiveSwaps(string(raw))
	result.Zram = active["/dev/zram0"]
	result.SD = active["/swapfile"]
	if !result.Zram.Enabled {
		result.Zram.SizeMiB = 64
	}
	if !result.SD.Enabled {
		result.SD.SizeMiB = 256
	}
	// Persisted size selections do not imply that activation actually succeeded.
	config, _ := os.ReadFile("/etc/kvm/memory.conf")
	result.Zram.Recompress = parseRecompressionSetting(string(config))
	_, recompErr := os.Stat("/sys/block/zram0/recompress")
	_, idleErr := os.Stat("/sys/block/zram0/idle")
	result.Zram.RecompressAvailable = recompErr == nil && idleErr == nil
	secondary, _ := os.ReadFile("/sys/block/zram0/recomp_algorithm")
	result.Zram.RecompressReady = result.Zram.Enabled && hasZstdRecompression(string(secondary))
	for _, line := range strings.Split(string(config), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		size, _ := strconv.ParseInt(value, 10, 64)
		if key == "ZRAM_SIZE" && !result.Zram.Enabled && validSwapRequest(memorySwapRequest{Kind: "zram", SizeMiB: size}) {
			result.Zram.SizeMiB = size
		}
		if key == "SD_SIZE" && !result.SD.Enabled && validSwapRequest(memorySwapRequest{Kind: "sd", SizeMiB: size}) {
			result.SD.SizeMiB = size
		}
	}
	_, helperErr := os.Stat(memoryService)
	result.SD.Available = helperErr == nil
	release, _ := os.ReadFile("/proc/sys/kernel/osrelease")
	modules, _ := filepath.Glob("/lib/modules/" + strings.TrimSpace(string(release)) + "/kernel/drivers/block/zram/zram.ko*")
	_, zramErr := os.Stat("/sys/module/zram")
	result.Zram.Available = helperErr == nil && (zramErr == nil || len(modules) > 0)
	result.Zram.Algorithm = "lz4"
	if mm, readErr := os.ReadFile("/sys/block/zram0/mm_stat"); readErr == nil {
		fields := strings.Fields(string(mm))
		if len(fields) >= 3 {
			result.Zram.OriginalBytes, _ = strconv.ParseUint(fields[0], 10, 64)
			result.Zram.CompressedBytes, _ = strconv.ParseUint(fields[1], 10, 64)
			result.Zram.MemoryBytes, _ = strconv.ParseUint(fields[2], 10, 64)
		}
	}
	if algo, readErr := os.ReadFile("/sys/block/zram0/comp_algorithm"); readErr == nil {
		for _, field := range strings.Fields(string(algo)) {
			if strings.HasPrefix(field, "[") {
				result.Zram.Algorithm = strings.Trim(field, "[]")
			}
		}
	}
	if ion, readErr := os.ReadFile("/sys/kernel/debug/ion/carveout/num_of_alloc_bytes"); readErr == nil {
		result.VideoBytes, _ = strconv.ParseUint(strings.TrimSpace(string(ion)), 10, 64)
	}
	return result, nil
}

func applyMemorySwap(req memorySwapRequest) error {
	if !validSwapRequest(req) {
		return fmt.Errorf("invalid swap type or size")
	}
	memoryMutation.Lock()
	defer memoryMutation.Unlock()
	enabled := "0"
	if req.Enabled {
		enabled = "1"
	}
	args := []string{memoryService, "configure", req.Kind, enabled, strconv.FormatInt(req.SizeMiB, 10)}
	if req.Recompress != nil {
		flag := "0"
		if *req.Recompress {
			flag = "1"
		}
		args = append(args, flag)
	}
	output, err := exec.Command("sh", args...).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if len(message) > 800 {
			message = message[len(message)-800:]
		}
		if message == "" {
			message = "Failed to change swap"
		}
		return fmt.Errorf("%s", message)
	}
	return nil
}

func (s *Service) GetMemoryStatus(c *gin.Context) {
	var rsp proto.Response
	status, err := readMemoryStatus()
	if err != nil {
		rsp.ErrRsp(c, -1, "Failed to read memory statistics")
		return
	}
	rsp.OkRspWithData(c, status)
}

func (s *Service) SetMemorySwap(c *gin.Context) {
	var rsp proto.Response
	var req memorySwapRequest
	if err := c.ShouldBindJSON(&req); err != nil || !validSwapRequest(req) {
		rsp.ErrRsp(c, -1, "Invalid swap type or size")
		return
	}
	if err := applyMemorySwap(req); err != nil {
		rsp.ErrRsp(c, -2, err.Error())
		return
	}
	s.GetMemoryStatus(c)
}
