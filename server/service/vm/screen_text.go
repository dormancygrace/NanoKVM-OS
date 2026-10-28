package vm

import (
	"bufio"
	"bytes"
	"cmp"
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
// region holds video and models together only up to that size. VPSS
// channel 2 also takes at most 1920 on either side.
const (
	screenTextMaxPixels = 1920 * 1080
	screenTextMaxSide   = 1920
)

// screenTextLanguages are the recognizers of the add-on, one directory of
// models each (addons/tpu/models/convert.sh); the first is the default.
var screenTextLanguages = []string{"latin", "chinese", "cyrillic"}

// screenTextLanguageFile holds the language a read without ?lang= uses
// (Settings > AI > Text recognition); absent: the first language.
var screenTextLanguageFile = "/etc/kvm/screen-text-language"

// ScreenTextLanguageNames are the recognizers a read can ask for.
func ScreenTextLanguageNames() []string { return slices.Clone(screenTextLanguages) }

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
// sides; 0x0 for inputs over Full HD or wider or taller than 1920.
func screenTextSize(width, height int) (int, int) {
	if width*height > screenTextMaxPixels || width > screenTextMaxSide || height > screenTextMaxSide {
		return 0, 0
	}
	return width &^ 1, height &^ 1
}

// ScreenTextArea is a part of the picture in pixels.
type ScreenTextArea struct {
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
func screenTextCropArea(f [4]float64, width, height int) ScreenTextArea {
	x0 := int(math.Floor(f[0]*float64(width))) - screenTextMargin
	y0 := int(math.Floor(f[1]*float64(height))) - screenTextMargin
	x1 := int(math.Ceil((f[0]+f[2])*float64(width))) + screenTextMargin
	y1 := int(math.Ceil((f[1]+f[3])*float64(height))) + screenTextMargin
	x0, y0 = max(0, min(x0, width-32)), max(0, min(y0, height-32))
	x1, y1 = min(width, max(x1, x0+32)), min(height, max(y1, y0+32))
	return ScreenTextArea{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

// cropPlanes cuts an area out of B, G and R planes of a width x height picture.
func cropPlanes(planes []byte, width, height int, area ScreenTextArea) []byte {
	out := make([]byte, 0, 3*area.W*area.H)
	for c := range 3 {
		plane := planes[c*width*height:]
		for y := area.Y; y < area.Y+area.H; y++ {
			out = append(out, plane[y*width+area.X:y*width+area.X+area.W]...)
		}
	}
	return out
}

// ScreenTextRequest is one read: Language "" is the default from the
// settings, Crop nil the whole picture (else x, y, w, h as fractions).
type ScreenTextRequest struct {
	Language    string
	Crop        *[4]float64
	SavePicture bool // keep the picture the helper read (?save=1)
}

// ScreenTextBox is a piece of a line the helper read, in pixels of the whole
// picture: the upright rectangle around it and its slant.
type ScreenTextBox struct {
	Text  string  `json:"text"`
	Score float64 `json:"score"`
	X     int     `json:"x"`
	Y     int     `json:"y"`
	W     int     `json:"w"`
	H     int     `json:"h"`
	Slope float64 `json:"slope"`
}

// ScreenTextReply is the text on the screen; Stats is the helper's summary
// line (words, re-read lines, detection and recognition time).
type ScreenTextReply struct {
	Language  string          `json:"language"`
	Width     int             `json:"width"`
	Height    int             `json:"height"`
	Area      ScreenTextArea  `json:"area"`
	Boxes     []ScreenTextBox `json:"boxes"`
	Stats     json.RawMessage `json:"stats"`
	CaptureMS int64           `json:"capture_ms"`
	TotalMS   int64           `json:"total_ms"`
}

// ScreenTextError is a read that did not happen; Code is the API's.
type ScreenTextError struct {
	Code    int
	Message string
}

func (e *ScreenTextError) Error() string { return e.Message }

func screenTextError(code int, format string, args ...any) error {
	return &ScreenTextError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// ReadScreenText reads the text on the screen with the TPU: one picture from
// VPSS as B, G and R planes, then nanokvm-ocr. Each line of the helper is one
// box; the last one holds the timings. A crop smaller than 1280 pixels wide
// is detected at full size, not halved, so small text is found, and it is
// faster. Boxes are in pixels of the whole picture either way. The web UI
// (GetScreenText), MCP and PicoClaw all read through here.
func ReadScreenText(ctx context.Context, req ScreenTextRequest) (*ScreenTextReply, error) {
	helper := ScreenTextHelper
	if _, err := os.Stat(helper); err != nil {
		return nil, screenTextError(-1, "the TPU add-on is not installed")
	}
	lang := req.Language
	if lang == "" {
		lang = screenTextDefaultLanguage()
	}
	args, err := screenTextArgs(screenTextRoot+"/models/", lang)
	if err != nil {
		return nil, screenTextError(-6, "%v", err)
	}
	if !screenTextMutex.TryLock() {
		return nil, screenTextError(-2, "the screen is being read")
	}
	defer screenTextMutex.Unlock()

	ctx, cancel := context.WithTimeout(ctx, screenTextLimit)
	defer cancel()
	inputWidth, inputHeight := common.CaptureInputSize()
	if inputWidth < 16 || inputHeight < 16 {
		return nil, screenTextError(-3, "no HDMI signal")
	}
	width, height := screenTextSize(inputWidth, inputHeight)
	if width == 0 {
		return nil, screenTextError(-5, "text recognition works up to 1920x1080; the input is %dx%d", inputWidth, inputHeight)
	}
	// The fresh-frame claim is left to the readers that can hand out a frame
	// buffered before HDMI resumed: VPSS channel 2 is set up for each read.
	release, _, err := AcquireHdmiCaptureLeaseForRead(ctx)
	if err != nil {
		return nil, screenTextError(-3, "%v", err)
	}
	start := time.Now()
	planes, err := readScreenPicture(ctx, width, height)
	release()
	if errors.Is(err, common.ErrPlanarPictureNoSignal) {
		return nil, screenTextError(-3, "no HDMI signal")
	}
	if err != nil {
		log.Warnf("screen text: %v", err)
		return nil, screenTextError(-8, "the picture of the screen could not be taken")
	}
	captured := time.Since(start)

	area := ScreenTextArea{W: width, H: height}
	if req.Crop != nil {
		area = screenTextCropArea(*req.Crop, width, height)
		planes = cropPlanes(planes, width, height, area)
	}
	input := make([]byte, 0, len(planes)+32)
	input = fmt.Appendf(input, "P %d %d\n", area.W, area.H)
	input = append(input, planes...)
	if req.SavePicture {
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
		return nil, screenTextError(-4, "text recognition failed")
	}
	boxes, done := parseScreenTextOutput(out, area.X, area.Y)
	return &ScreenTextReply{
		Language: lang, Width: width, Height: height, Area: area, Boxes: boxes, Stats: done,
		CaptureMS: captured.Milliseconds(), TotalMS: time.Since(start).Milliseconds(),
	}, nil
}

// readScreenPicture takes the picture, trying again when VPSS gave no frame in
// time or a new input size was being detected: a capture started cold (no
// viewer since the server started, as for MCP) can take more than a second for
// its first frame. Each native call waits at most half a second for the
// picture with the capture locked (the pool and the copy add a little); the
// video readers wait a second for that lock, and get it between tries. A
// detection returns at once, so the pause after it is as long as that wait.
func readScreenPicture(ctx context.Context, width, height int) ([]byte, error) {
	var err error
	for attempt := 0; attempt < 6; attempt++ {
		var planes []byte
		planes, err = common.GetKvmVision().ReadBGRPlanar(width, height)
		pause := 100 * time.Millisecond
		switch {
		case errors.Is(err, common.ErrPlanarCaptureNotReady):
			pause = 500 * time.Millisecond
		case !errors.Is(err, common.ErrPlanarPictureTimeout):
			return planes, err
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(pause):
		}
	}
	return nil, err
}

// parseScreenTextOutput reads the helper's lines: the boxes, moved by dx, dy
// from the area into the picture, and the summary.
func parseScreenTextOutput(out []byte, dx, dy int) ([]ScreenTextBox, json.RawMessage) {
	boxes := []ScreenTextBox{}
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
		var box ScreenTextBox
		if err := json.Unmarshal(line, &box); err != nil {
			continue
		}
		box.X += dx
		box.Y += dy
		// The helper reads a line at the top edge with its margin above the
		// picture; report the part inside it (MCP promises 0..1).
		if box.Y < 0 {
			box.H = max(1, box.H+box.Y)
			box.Y = 0
		}
		boxes = append(boxes, box)
	}
	return boxes, done
}

// GetScreenText is ReadScreenText for the web UI: ?lang= picks the
// recognizer (screenTextLanguages), ?crop=x,y,w,h (fractions of the picture)
// an area, ?save=1 keeps the picture.
func (s *Service) GetScreenText(c *gin.Context) {
	var rsp proto.Response
	req := ScreenTextRequest{Language: c.Query("lang"), SavePicture: c.Query("save") == "1"}
	if value := c.Query("crop"); value != "" {
		f, err := parseScreenTextCrop(value)
		if err != nil {
			rsp.ErrRsp(c, -7, err.Error())
			return
		}
		req.Crop = &f
	}
	reply, err := ReadScreenText(c.Request.Context(), req)
	if err != nil {
		code := -4
		var readErr *ScreenTextError
		if errors.As(err, &readErr) {
			code = readErr.Code
		}
		rsp.ErrRsp(c, code, err.Error())
		return
	}
	rsp.OkRspWithData(c, reply)
}

// ScreenTextLines is the text in reading order. The helper gives the pieces
// of a detected line one after another, left to right: those chain into
// runs; runs side by side (a row of a table, a line the detector cut) join
// into one line; lines go top to bottom by their first box. Neighbours only:
// lines on photos are slanted, and lines of small text are closer together
// than the boxes are high. (The web UI does the same: lib/screen-text.ts.)
func ScreenTextLines(boxes []ScreenTextBox) string {
	centre := func(b ScreenTextBox) float64 { return float64(b.Y) + float64(b.H)/2 }
	// b continues a: it starts past the middle of a, and their centres are
	// within half the lower box's height.
	follows := func(a, b ScreenTextBox) bool {
		return float64(b.X) >= float64(a.X)+float64(a.W)/2 &&
			math.Abs(centre(a)-centre(b)) <= float64(min(a.H, b.H))/2
	}
	var runs [][]ScreenTextBox
	for _, box := range boxes {
		if n := len(runs); n > 0 && follows(runs[n-1][len(runs[n-1])-1], box) {
			runs[n-1] = append(runs[n-1], box)
		} else {
			runs = append(runs, []ScreenTextBox{box})
		}
	}
	byFirst := func(a, b []ScreenTextBox) int { return cmp.Compare(centre(a[0]), centre(b[0])) }
	slices.SortStableFunc(runs, byFirst)
	var lines [][]ScreenTextBox
	for _, run := range runs {
		joined := false
		for i, line := range lines {
			if follows(line[len(line)-1], run[0]) {
				lines[i] = append(line, run...)
				joined = true
				break
			}
		}
		if !joined {
			for i, line := range lines {
				if follows(run[len(run)-1], line[0]) {
					lines[i] = append(append([]ScreenTextBox(nil), run...), line...)
					joined = true
					break
				}
			}
		}
		if !joined {
			lines = append(lines, run)
		}
	}
	slices.SortStableFunc(lines, byFirst)
	text := make([]string, len(lines))
	for i, line := range lines {
		words := make([]string, len(line))
		for j, box := range line {
			words[j] = box.Text
		}
		text[i] = strings.Join(words, " ")
	}
	return strings.Join(text, "\n")
}
