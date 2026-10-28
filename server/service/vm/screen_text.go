package vm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/common"
	"NanoKVM-Server/internal/atomicfile"
	"NanoKVM-Server/proto"
)

// The TPU add-on (addons/tpu) installs the helper and its models here.
const screenTextRoot = "/usr/lib/nanokvm-tpu"

// ScreenTextHelper is the add-on's program; its presence means the add-on
// is installed.
const ScreenTextHelper = screenTextRoot + "/bin/nanokvm-ocr"

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
var screenTextLanguages = []string{"latin", "chinese", "cyrillic"}

// screenTextLanguageFile holds the language a read without ?lang= uses
// (Settings > AI > Text recognition); absent: the first language.
var screenTextLanguageFile = "/etc/kvm/screen-text-language"

func screenTextDefaultLanguage() string {
	data, err := os.ReadFile(screenTextLanguageFile)
	if lang := strings.TrimSpace(string(data)); err == nil && slices.Contains(screenTextLanguages, lang) {
		return lang
	}
	return screenTextLanguages[0]
}

// GetScreenTextSettings: the default language and which languages have
// their models installed.
func (s *Service) GetScreenTextSettings(c *gin.Context) {
	var rsp proto.Response
	type language struct {
		Name      string `json:"name"`
		Installed bool   `json:"installed"`
	}
	languages := make([]language, 0, len(screenTextLanguages))
	for _, lang := range screenTextLanguages {
		_, err := screenTextArgs(screenTextRoot+"/models/", lang)
		languages = append(languages, language{Name: lang, Installed: err == nil})
	}
	rsp.OkRspWithData(c, gin.H{"language": screenTextDefaultLanguage(), "languages": languages})
}

// SetScreenTextSettings saves the default language ({"language":"..."}).
func (s *Service) SetScreenTextSettings(c *gin.Context) {
	var rsp proto.Response
	var req struct {
		Language string `json:"language"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || !slices.Contains(screenTextLanguages, req.Language) {
		rsp.ErrRsp(c, -1, "unknown language")
		return
	}
	if err := atomicfile.Write(screenTextLanguageFile, []byte(req.Language+"\n"), 0o644); err != nil {
		log.Errorf("screen text: save language: %v", err)
		rsp.ErrRsp(c, -2, "the language could not be saved")
		return
	}
	rsp.OkRspWithData(c, gin.H{"language": req.Language})
}

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

// screenTextArea is a part of the picture in pixels.
type screenTextArea struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// screenTextMargin is added around a requested area, so a line the
// selection cuts at its edge is still detected whole.
const screenTextMargin = 24

// parseScreenTextCrop reads ?crop=x,y,w,h, fractions (0..1) of the picture.
func parseScreenTextCrop(value string) ([4]float64, error) {
	var f [4]float64
	parts := strings.Split(value, ",")
	if len(parts) != 4 {
		return f, errors.New("crop is x,y,w,h")
	}
	for i, part := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil || math.IsNaN(v) || v < 0 || v > 1 {
			return f, errors.New("crop values are fractions from 0 to 1")
		}
		f[i] = v
	}
	if f[2] <= 0 || f[3] <= 0 {
		return f, errors.New("crop is empty")
	}
	return f, nil
}

// screenTextCropArea is the area to read for a crop of a width x height
// picture: the fractions in pixels, the margin around them, within the
// picture, at least 32x32.
func screenTextCropArea(f [4]float64, width, height int) screenTextArea {
	x0 := int(math.Floor(f[0]*float64(width))) - screenTextMargin
	y0 := int(math.Floor(f[1]*float64(height))) - screenTextMargin
	x1 := int(math.Ceil((f[0]+f[2])*float64(width))) + screenTextMargin
	y1 := int(math.Ceil((f[1]+f[3])*float64(height))) + screenTextMargin
	x0, y0 = max(0, min(x0, width-32)), max(0, min(y0, height-32))
	x1, y1 = min(width, max(x1, x0+32)), min(height, max(y1, y0+32))
	return screenTextArea{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

// cropPlanes cuts an area out of B, G and R planes of a width x height picture.
func cropPlanes(planes []byte, width, height int, area screenTextArea) []byte {
	out := make([]byte, 0, 3*area.W*area.H)
	for c := range 3 {
		plane := planes[c*width*height:]
		for y := area.Y; y < area.Y+area.H; y++ {
			out = append(out, plane[y*width+area.X:y*width+area.X+area.W]...)
		}
	}
	return out
}

// shiftScreenTextBox moves a box of the helper by dx, dy (from the area into
// the picture). The struct holds the helper's fields (nanokvm-ocr.cpp), in
// its order.
func shiftScreenTextBox(line []byte, dx, dy int) ([]byte, error) {
	var box struct {
		Text  string  `json:"text"`
		Score float64 `json:"score"`
		X     int     `json:"x"`
		Y     int     `json:"y"`
		W     int     `json:"w"`
		H     int     `json:"h"`
		Slope float64 `json:"slope"`
	}
	if err := json.Unmarshal(line, &box); err != nil {
		return nil, err
	}
	box.X += dx
	box.Y += dy
	return json.Marshal(box)
}

// GetScreenText reads the text on the screen with the TPU: one picture from
// VPSS as B, G and R planes, then nanokvm-ocr. Each line of the helper is one
// box ({"text","score","x","y","w","h","slope"}); the last one holds the
// timings. ?lang= picks the recognizer (screenTextLanguages; without it the
// default from the settings). ?crop=x,y,w,h
// (fractions of the picture) reads only that area: smaller than 1280 pixels
// wide, it is detected at full size, not halved, so small text is found, and
// it is faster. Boxes are in pixels of the whole picture either way.
func (s *Service) GetScreenText(c *gin.Context) {
	var rsp proto.Response
	helper := ScreenTextHelper
	if _, err := os.Stat(helper); err != nil {
		rsp.ErrRsp(c, -1, "the TPU add-on is not installed")
		return
	}
	lang := c.Query("lang")
	if lang == "" {
		lang = screenTextDefaultLanguage()
	}
	args, err := screenTextArgs(screenTextRoot+"/models/", lang)
	if err != nil {
		rsp.ErrRsp(c, -6, err.Error())
		return
	}
	var crop *[4]float64
	if value := c.Query("crop"); value != "" {
		f, err := parseScreenTextCrop(value)
		if err != nil {
			rsp.ErrRsp(c, -7, err.Error())
			return
		}
		crop = &f
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

	area := screenTextArea{W: width, H: height}
	if crop != nil {
		area = screenTextCropArea(*crop, width, height)
		planes = cropPlanes(planes, width, height, area)
	}
	input := make([]byte, 0, len(planes)+32)
	input = fmt.Appendf(input, "P %d %d\n", area.W, area.H)
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
		if area.X != 0 || area.Y != 0 {
			shifted, err := shiftScreenTextBox(line, area.X, area.Y)
			if err != nil {
				continue
			}
			line = shifted
		}
		boxes = append(boxes, append(json.RawMessage(nil), line...))
	}
	rsp.OkRspWithData(c, gin.H{
		"width": width, "height": height, "area": area, "boxes": boxes, "stats": done,
		"capture_ms": captured.Milliseconds(), "total_ms": time.Since(start).Milliseconds(),
	})
}
