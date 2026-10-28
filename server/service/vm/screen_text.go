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
	"slices"
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

// screenTextMaxPixels: the TPU reads screens up to Full HD (1920x1080 or
// 1080x1920). Larger inputs take video buffers the models need: the CMA
// region holds video and models together only up to that size.
const screenTextMaxPixels = 1920 * 1080

// screenTextLanguages are the recognizers of the add-on, one directory of
// models each (addons/tpu/models/convert.sh); the first is the default.
var screenTextLanguages = []string{"latin", "cyrillic"}

// screenTextArgs are the helper's arguments for a language: the recognizer's
// three widths are programs of one model. A language may have a surer model
// with the same dictionary (Latin: the medium one), which reads again the
// lines the small one is unsure of.
func screenTextArgs(models, lang string) ([]string, error) {
	if lang == "" {
		lang = screenTextLanguages[0]
	}
	if !slices.Contains(screenTextLanguages, lang) {
		return nil, fmt.Errorf("unknown language %q", lang)
	}
	dir := models + lang + "/"
	if _, err := os.Stat(dir + "ocr-rec.cvimodel"); err != nil {
		return nil, fmt.Errorf("no models for %s", lang)
	}
	args := []string{models + "ocr-det.cvimodel", dir + "ocr-rec.cvimodel:3", dir + "ocr-dict.txt"}
	if _, err := os.Stat(dir + "ocr-sure.cvimodel"); err == nil {
		args = append(args, dir+"ocr-sure.cvimodel:3")
	}
	return args, nil
}

// screenTextSize is the planar picture for an input: the input itself, even
// sides; 0x0 for inputs over Full HD.
func screenTextSize(width, height int) (int, int) {
	if width*height > screenTextMaxPixels {
		return 0, 0
	}
	return width &^ 1, height &^ 1
}

// GetScreenText reads the text on the screen with the TPU: one picture from
// VPSS as B, G and R planes, then nanokvm-ocr. Each line of the helper is one
// box ({"text","score","x","y","w","h"}); the last one holds the timings.
// ?lang= picks the recognizer (screenTextLanguages).
func (s *Service) GetScreenText(c *gin.Context) {
	var rsp proto.Response
	helper := screenTextRoot + "/bin/nanokvm-ocr"
	if _, err := os.Stat(helper); err != nil {
		rsp.ErrRsp(c, -1, "the TPU add-on is not installed")
		return
	}
	args, err := screenTextArgs(screenTextRoot+"/models/", c.Query("lang"))
	if err != nil {
		rsp.ErrRsp(c, -6, err.Error())
		return
	}
	if !screenTextMutex.TryLock() {
		rsp.ErrRsp(c, -2, "the screen is being read")
		return
	}
	defer screenTextMutex.Unlock()

	ctx, cancel := context.WithTimeout(c.Request.Context(), screenTextLimit)
	defer cancel()
	inputWidth, inputHeight := common.CaptureInputSize()
	if inputWidth < 16 || inputHeight < 16 {
		rsp.ErrRsp(c, -3, "no HDMI signal")
		return
	}
	width, height := screenTextSize(inputWidth, inputHeight)
	if width == 0 {
		rsp.ErrRsp(c, -5, fmt.Sprintf("text recognition works up to 1920x1080; the input is %dx%d", inputWidth, inputHeight))
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

	cmd := exec.CommandContext(ctx, helper, args...)
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
