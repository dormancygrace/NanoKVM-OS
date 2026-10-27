package vm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/common"
	"NanoKVM-Server/proto"
)

// The TPU add-on (addons/tpu) installs the helper and its models here.
var screenTextRoot = "/usr/lib/nanokvm-tpu"

// screenTextPicture keeps the last picture for offline runs (?save=1).
var screenTextPicture = "/run/nanokvm/screen-text.bgr"

// One reader at a time: the models take most of the TPU memory video leaves.
var screenTextMutex sync.Mutex

// screenTextLimit bounds one read: models, detection and every line.
const screenTextLimit = 90 * time.Second

// screenTextSize scales the input to the planar picture: at most 1920 wide
// (VPSS channel 2), even sides, the aspect ratio kept.
func screenTextSize(width, height int) (int, int) {
	if width > 1920 {
		height = height * 1920 / width
		width = 1920
	}
	return width &^ 1, height &^ 1
}

// GetScreenText reads the text on the screen with the TPU: one picture from
// VPSS as B, G and R planes, then nanokvm-ocr. Each line of the helper is one
// box ({"text","score","x","y","w","h"}); the last one holds the timings.
func (s *Service) GetScreenText(c *gin.Context) {
	var rsp proto.Response
	helper := screenTextRoot + "/bin/nanokvm-ocr"
	models := screenTextRoot + "/models/"
	if _, err := os.Stat(helper); err != nil {
		rsp.ErrRsp(c, -1, "the TPU add-on is not installed")
		return
	}
	if !screenTextMutex.TryLock() {
		rsp.ErrRsp(c, -2, "the screen is being read")
		return
	}
	defer screenTextMutex.Unlock()

	ctx, cancel := context.WithTimeout(c.Request.Context(), screenTextLimit)
	defer cancel()
	width, height := screenTextSize(common.CaptureInputSize())
	if width < 16 || height < 16 {
		rsp.ErrRsp(c, -3, "no HDMI signal")
		return
	}
	release, claimFresh, err := AcquireHdmiCaptureLeaseForRead(ctx)
	if err != nil {
		rsp.ErrRsp(c, -3, err.Error())
		return
	}
	if claimFresh != nil {
		claimFresh()
	}
	start := time.Now()
	planes, err := common.GetKvmVision().ReadBGRPlanar(width, height)
	release()
	if err != nil {
		rsp.ErrRsp(c, -3, err.Error())
		return
	}
	captured := time.Since(start)

	input := make([]byte, 0, len(planes)+32)
	input = fmt.Appendf(input, "P %d %d\n", width, height)
	input = append(input, planes...)
	if c.Query("save") == "1" {
		if err := os.WriteFile(screenTextPicture, input, 0o600); err != nil {
			log.Warnf("screen text: keep picture: %v", err)
		}
	}

	cmd := exec.CommandContext(ctx, helper, models+"ocr-det.cvimodel",
		models+"ocr-rec-160.cvimodel,"+models+"ocr-rec-320.cvimodel,"+models+"ocr-rec-640.cvimodel",
		models+"ocr-dict.txt")
	cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH="+screenTextRoot+"/lib")
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = ctx.Err()
		}
		log.Errorf("screen text: %v: %s", err, bytes.TrimSpace(stderr.Bytes()))
		rsp.ErrRsp(c, -4, "text recognition failed")
		return
	}

	boxes := []json.RawMessage{}
	var done json.RawMessage
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 || !json.Valid(line) {
			continue
		}
		if bytes.HasPrefix(line, []byte(`{"done":`)) {
			done = append(json.RawMessage(nil), line...)
			continue
		}
		boxes = append(boxes, append(json.RawMessage(nil), line...))
	}
	rsp.OkRspWithData(c, gin.H{
		"width": width, "height": height, "boxes": boxes, "stats": done,
		"capture_ms": captured.Milliseconds(), "total_ms": time.Since(start).Milliseconds(),
	})
}
