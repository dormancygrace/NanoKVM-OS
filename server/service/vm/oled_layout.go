package vm

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"NanoKVM-Server/internal/atomicfile"
	"NanoKVM-Server/proto"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// The OLED main page layout (kvm_system's lib/oled_layout). The editor sends
// and receives proto.OLEDLayout; kvm_system reads the line format written
// here. kvm_system writes its built-in layout, which also names the panel.
var (
	oledLayoutFile  = "/etc/kvm/oled_layout"
	oledPreviewFile = "/run/nanokvm/oled_preview"
	oledDefaultFile = "/run/nanokvm/oled_default"
)

// The limits of kvm_system's parser.
const (
	oledMaxItems      = 128
	oledMaxText       = 128
	oledMaxConditions = 4
	oledMaxPages      = 4
	oledMaxBytes      = 65536 // the file, as kvm_system's read_layout_file reads it
	oledLow           = -128
	oledHigh          = 255
)

var oledConditions = map[string]bool{"eth": true, "wifi": true, "hdmi": true, "hid": true, "stream": true,
	"wifi_module": true, "atx": true, "page1": true, "page2": true, "page3": true, "page4": true}

var oledVariables = map[string]bool{"ip": true, "eth_ip": true, "wifi_ip": true, "hostname": true, "codec": true,
	"width": true, "height": true, "fps": true, "viewers": true, "uptime": true, "time": true, "date": true}

var oledWidth = regexp.MustCompile(`^-?[0-9]{1,2}$`)

// The {name:N} widths of known variables, scanned as kvm_system scans them:
// -32..32, an optional minus and at most two digits.
func oledWidthsValid(text string) bool {
	for i := 0; i < len(text); i++ {
		end := strings.IndexByte(text[i:], '}')
		if text[i] != '{' || end < 0 {
			continue
		}
		name, number, padded := strings.Cut(text[i+1:i+end], ":")
		if !oledVariables[name] {
			continue
		}
		if padded {
			width, err := strconv.Atoi(number)
			if !oledWidth.MatchString(number) || err != nil || width < -32 || width > 32 {
				return false
			}
		}
		i += end
	}
	return true
}

func oledBool(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func oledCoordinates(values ...int) error {
	for _, value := range values {
		if value < oledLow || value > oledHigh {
			return errors.New("coordinate out of range")
		}
	}
	return nil
}

func oledWhen(when string) (string, error) {
	if when == "" {
		return "-", nil
	}
	names := strings.Split(when, ",")
	if len(names) > oledMaxConditions {
		return "", fmt.Errorf("more than %d conditions", oledMaxConditions)
	}
	for _, name := range names {
		if !oledConditions[strings.TrimPrefix(name, "!")] {
			return "", fmt.Errorf("unknown condition %q", name)
		}
	}
	return when, nil
}

// compileOLEDLayout checks a layout and returns the file kvm_system reads.
func compileOLEDLayout(layout *proto.OLEDLayout) (string, error) {
	if layout.Panel != "wide" && layout.Panel != "narrow" {
		return "", errors.New("unknown panel")
	}
	if layout.Rotation%90 != 0 || layout.Rotation < 0 || layout.Rotation > 270 {
		return "", errors.New("invalid rotation")
	}
	if len(layout.Items) > oledMaxItems {
		return "", fmt.Errorf("more than %d items", oledMaxItems)
	}
	var out strings.Builder
	fmt.Fprintf(&out, "nanokvm-oled 1 %s\nrotation %d\n", layout.Panel, layout.Rotation)
	switch layout.Burnin {
	case "", "none", "shift":
		mode := layout.Burnin
		if mode == "" {
			mode = "none"
		}
		fmt.Fprintf(&out, "burnin %s\n", mode)
	case "roam":
		if layout.RoamWidth < 1 || layout.RoamWidth > 128 || layout.RoamHeight < 1 || layout.RoamHeight > 128 {
			return "", errors.New("invalid roaming block")
		}
		fmt.Fprintf(&out, "burnin roam %d %d\n", layout.RoamWidth, layout.RoamHeight)
	default:
		return "", errors.New("unknown burn-in protection")
	}
	if layout.Pages > 1 {
		if layout.Pages > oledMaxPages || layout.PageSeconds < 1 || layout.PageSeconds > 60 {
			return "", errors.New("invalid pages")
		}
		fmt.Fprintf(&out, "pages %d %d\n", layout.Pages, layout.PageSeconds)
	}
	for i, item := range layout.Items {
		line, err := compileOLEDItem(&item)
		if err != nil {
			return "", fmt.Errorf("item %d: %w", i+1, err)
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	if out.Len() > oledMaxBytes {
		return "", fmt.Errorf("the layout is larger than %d bytes", oledMaxBytes)
	}
	return out.String(), nil
}

func compileOLEDItem(item *proto.OLEDItem) (string, error) {
	when, err := oledWhen(item.When)
	if err != nil {
		return "", err
	}
	switch item.Type {
	case "text":
		if item.Font != 4 && item.Font != 8 && item.Font != 16 {
			return "", errors.New("invalid font")
		}
		if item.Align != "left" && item.Align != "center" && item.Align != "right" {
			return "", errors.New("invalid alignment")
		}
		if item.Width < 0 || item.Width > 128 {
			return "", errors.New("invalid text width")
		}
		if len(item.Text) > oledMaxText {
			return "", errors.New("text too long")
		}
		for _, chr := range item.Text {
			if chr < ' ' || chr > '~' {
				return "", errors.New("text must be printable ASCII")
			}
		}
		if !oledWidthsValid(item.Text) {
			return "", errors.New("a variable width must be -32 to 32")
		}
		if err := oledCoordinates(item.X, item.Y); err != nil {
			return "", err
		}
		return fmt.Sprintf("text %d %d %d %s %d %s %s %s", item.X, item.Y, item.Font, item.Align, item.Width,
			oledBool(!item.Off), when, item.Text), nil
	case "line":
		if err := oledCoordinates(item.X, item.Y, item.X2, item.Y2); err != nil {
			return "", err
		}
		return fmt.Sprintf("line %d %d %d %d %s %s", item.X, item.Y, item.X2, item.Y2, oledBool(!item.Off), when), nil
	case "rect", "image":
		if err := oledCoordinates(item.X, item.Y); err != nil {
			return "", err
		}
		if item.Width < 1 || item.Width > 128 || item.Height < 1 || item.Height > 128 {
			return "", errors.New("invalid size")
		}
		if item.Type == "rect" {
			return fmt.Sprintf("rect %d %d %d %d %s %s %s", item.X, item.Y, item.Width, item.Height,
				oledBool(item.Fill), oledBool(!item.Off), when), nil
		}
		data, err := hex.DecodeString(item.Data)
		if err != nil || len(data) != (item.Width+7)/8*item.Height {
			return "", errors.New("image data does not match its size")
		}
		return fmt.Sprintf("image %d %d %d %d %s %s %s", item.X, item.Y, item.Width, item.Height,
			oledBool(!item.Off), when, hex.EncodeToString(data)), nil
	}
	return "", fmt.Errorf("unknown item %q", item.Type)
}

// parseOLEDLayout reads the line format back, for the saved and the
// built-in layout. Fields are separated by single spaces; a text keeps
// everything after its condition.
func parseOLEDLayout(text string) (*proto.OLEDLayout, error) {
	layout := &proto.OLEDLayout{Items: []proto.OLEDItem{}}
	header := false
	for number, line := range strings.Split(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" || line[0] == '#' {
			continue
		}
		fail := fmt.Errorf("line %d", number+1)
		fields := strings.Split(line, " ")
		if !header {
			if len(fields) != 3 || fields[0] != "nanokvm-oled" || fields[1] != "1" {
				return nil, fail
			}
			layout.Panel = fields[2]
			header = true
			continue
		}
		numbers := func(count int) ([]int, bool) {
			if len(fields) < count+1 {
				return nil, false
			}
			values := make([]int, count)
			for i := range values {
				value, err := strconv.Atoi(fields[i+1])
				if err != nil {
					return nil, false
				}
				values[i] = value
			}
			return values, true
		}
		switch fields[0] {
		case "rotation":
			values, ok := numbers(1)
			if !ok {
				return nil, fail
			}
			layout.Rotation = values[0]
		case "burnin":
			if len(fields) < 2 {
				return nil, fail
			}
			layout.Burnin = fields[1]
			if fields[1] == "roam" {
				fields = fields[1:]
				values, ok := numbers(2)
				if !ok {
					return nil, fail
				}
				layout.RoamWidth, layout.RoamHeight = values[0], values[1]
			}
		case "pages":
			values, ok := numbers(2)
			if !ok || len(fields) != 3 {
				return nil, fail
			}
			layout.Pages, layout.PageSeconds = values[0], values[1]
		case "text":
			values, ok := numbers(3)
			if !ok || len(fields) < 8 {
				return nil, fail
			}
			width, err := strconv.Atoi(fields[5])
			if err != nil {
				return nil, fail
			}
			layout.Items = append(layout.Items, proto.OLEDItem{Type: "text", X: values[0], Y: values[1],
				Font: values[2], Align: fields[4], Width: width, Off: fields[6] == "0",
				When: parsedWhen(fields[7]), Text: strings.Join(fields[8:], " ")})
		case "line":
			values, ok := numbers(4)
			if !ok || len(fields) != 7 {
				return nil, fail
			}
			layout.Items = append(layout.Items, proto.OLEDItem{Type: "line", X: values[0], Y: values[1],
				X2: values[2], Y2: values[3], Off: fields[5] == "0", When: parsedWhen(fields[6])})
		case "rect":
			values, ok := numbers(4)
			if !ok || len(fields) != 8 {
				return nil, fail
			}
			layout.Items = append(layout.Items, proto.OLEDItem{Type: "rect", X: values[0], Y: values[1],
				Width: values[2], Height: values[3], Fill: fields[5] == "1", Off: fields[6] == "0",
				When: parsedWhen(fields[7])})
		case "image":
			values, ok := numbers(4)
			if !ok || len(fields) != 8 {
				return nil, fail
			}
			layout.Items = append(layout.Items, proto.OLEDItem{Type: "image", X: values[0], Y: values[1],
				Width: values[2], Height: values[3], Off: fields[5] == "0", When: parsedWhen(fields[6]),
				Data: fields[7]})
		default:
			return nil, fail
		}
	}
	if !header {
		return nil, errors.New("empty layout")
	}
	// What kvm_system would refuse is refused here as well.
	if _, err := compileOLEDLayout(layout); err != nil {
		return nil, err
	}
	return layout, nil
}

func parsedWhen(field string) string {
	if field == "-" {
		return ""
	}
	return field
}

func readOLEDLayout(path string) (*proto.OLEDLayout, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) > oledMaxBytes {
		return nil, errors.New("layout file too large")
	}
	return parseOLEDLayout(string(data))
}

// Each write has its own temporary file, so overlapping ones (the preview
// resent, two editors) never publish a part of a layout.
func writeOLEDFile(path string, data string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return atomicfile.Write(path, []byte(data), mode)
}

// The request's layout, checked for this device's panel.
func oledLayoutRequest(c *gin.Context) (string, bool) {
	var rsp proto.Response
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256*1024)
	var req proto.SetOLEDLayoutReq
	if err := c.ShouldBindJSON(&req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return "", false
	}
	builtIn, err := readOLEDLayout(oledDefaultFile)
	if err != nil {
		rsp.ErrRsp(c, -2, "the OLED is not available")
		return "", false
	}
	if req.Layout.Panel != builtIn.Panel {
		rsp.ErrRsp(c, -1, "the layout is for the other panel")
		return "", false
	}
	text, err := compileOLEDLayout(&req.Layout)
	if err != nil {
		rsp.ErrRsp(c, -1, "invalid layout: "+err.Error())
		return "", false
	}
	return text, true
}

func (s *Service) GetOLEDLayout(c *gin.Context) {
	var rsp proto.Response
	builtIn, err := readOLEDLayout(oledDefaultFile)
	if err != nil {
		rsp.OkRspWithData(c, &proto.GetOLEDLayoutRsp{})
		return
	}
	data := &proto.GetOLEDLayoutRsp{Panel: builtIn.Panel, BuiltIn: builtIn}
	if saved, err := readOLEDLayout(oledLayoutFile); err == nil && saved.Panel == builtIn.Panel {
		data.Saved = saved
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Warnf("OLED layout not used: %s", err)
	}
	rsp.OkRspWithData(c, data)
}

func (s *Service) SetOLEDLayout(c *gin.Context) {
	var rsp proto.Response
	text, ok := oledLayoutRequest(c)
	if !ok {
		return
	}
	if err := writeOLEDFile(oledLayoutFile, text, 0o644); err != nil {
		rsp.ErrRsp(c, -2, "failed to save the layout")
		return
	}
	rsp.OkRsp(c)
}

func (s *Service) DeleteOLEDLayout(c *gin.Context) {
	var rsp proto.Response
	if err := os.Remove(oledLayoutFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		rsp.ErrRsp(c, -2, "failed to remove the layout")
		return
	}
	rsp.OkRsp(c)
}

// The editor sends the preview again every few seconds; kvm_system shows it
// while it is fresh, so a closed editor ends it.
func (s *Service) SetOLEDPreview(c *gin.Context) {
	var rsp proto.Response
	text, ok := oledLayoutRequest(c)
	if !ok {
		return
	}
	if err := writeOLEDFile(oledPreviewFile, text, 0o644); err != nil {
		rsp.ErrRsp(c, -2, "failed to write the preview")
		return
	}
	rsp.OkRsp(c)
}

func (s *Service) DeleteOLEDPreview(c *gin.Context) {
	var rsp proto.Response
	if err := os.Remove(oledPreviewFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		rsp.ErrRsp(c, -2, "failed to end the preview")
		return
	}
	rsp.OkRsp(c)
}
