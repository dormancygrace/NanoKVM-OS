package common

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// The watermark: up to WatermarkMaxBlocks pictures the VPSS draws into every
// stream (H.264, H.265, MJPEG, screenshots, recordings and RustDesk). The
// browser composes each block as a PNG for a picture WatermarkRefWidth pixels
// wide; the native library scales it to each output and places it at a
// fraction of the free room inside the margin. The region engine works on
// VPSS outputs only, so while it is on no stream is taken straight from
// capture.
const (
	WatermarkRefWidth  = 1920
	WatermarkMaxBlocks = 8
	// One block at most a quarter of the reference picture's width and
	// height, and all blocks together no more pixels than one such block:
	// each is ARGB8888 in video memory, per output, scaled with the stream
	// (about 4 MiB per output at 2160p).
	WatermarkMaxWidth  = 960
	WatermarkMaxHeight = 270
	WatermarkMaxArea   = WatermarkMaxWidth * WatermarkMaxHeight
	watermarkMaxBytes  = 1 << 20
	watermarkMaxMargin = 200
	watermarkMaxDesign = 8192
	rgnDevice          = "/dev/cvi-rgn"
)

var (
	WatermarkDir   = "/etc/kvm/watermark"
	watermarkMutex sync.Mutex
)

// WatermarkBlock is one block's place: X and Y in thousandths of the free
// room inside the margin (0 left or top, 500 centre, 1000 right or bottom).
type WatermarkBlock struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// WatermarkSettings is the saved choice. Design is the web editor's own
// description of the blocks (text, fonts, colours), kept for editing them
// again; the server does not read it.
type WatermarkSettings struct {
	Enabled bool             `json:"enabled"`
	Margin  int              `json:"margin"`
	Design  string           `json:"design"`
	Blocks  []WatermarkBlock `json:"blocks"`
}

func DefaultWatermarkSettings() WatermarkSettings {
	return WatermarkSettings{Margin: 24, Blocks: []WatermarkBlock{}}
}

func (s WatermarkSettings) Validate() error {
	if s.Margin < 0 || s.Margin > watermarkMaxMargin {
		return fmt.Errorf("margin must be 0-%d", watermarkMaxMargin)
	}
	if len(s.Design) > watermarkMaxDesign {
		return fmt.Errorf("design must be at most %d bytes", watermarkMaxDesign)
	}
	if len(s.Blocks) > WatermarkMaxBlocks {
		return fmt.Errorf("at most %d blocks", WatermarkMaxBlocks)
	}
	for _, b := range s.Blocks {
		if b.X < 0 || b.X > 1000 || b.Y < 0 || b.Y > 1000 {
			return errors.New("block places must be 0-1000")
		}
	}
	return nil
}

func watermarkSettingsFile() string { return filepath.Join(WatermarkDir, "settings.json") }
func watermarkBlockFile(i int) string {
	return filepath.Join(WatermarkDir, fmt.Sprintf("block-%d.png", i))
}

// LoadWatermark returns the saved settings and each block's picture (nil
// where missing).
func LoadWatermark() (WatermarkSettings, [][]byte) {
	settings := DefaultWatermarkSettings()
	if raw, err := os.ReadFile(watermarkSettingsFile()); err == nil {
		var saved WatermarkSettings
		if json.Unmarshal(raw, &saved) == nil && saved.Validate() == nil {
			settings = saved
			if settings.Blocks == nil {
				settings.Blocks = []WatermarkBlock{}
			}
		}
	}
	pictures := make([][]byte, len(settings.Blocks))
	for i := range settings.Blocks {
		if picture, err := os.ReadFile(watermarkBlockFile(i)); err == nil {
			pictures[i] = picture
		}
	}
	return settings, pictures
}

// DecodeWatermark checks a block's PNG and returns its pixels.
func DecodeWatermark(picture []byte) (*image.NRGBA, error) {
	if len(picture) == 0 || len(picture) > watermarkMaxBytes {
		return nil, errors.New("a block must be a PNG of at most 1 MiB")
	}
	config, err := png.DecodeConfig(bytes.NewReader(picture))
	if err != nil {
		return nil, errors.New("a block must be a PNG")
	}
	if config.Width < 2 || config.Height < 2 || config.Width > WatermarkMaxWidth || config.Height > WatermarkMaxHeight {
		return nil, fmt.Errorf("a block must be 2x2 to %dx%d pixels", WatermarkMaxWidth, WatermarkMaxHeight)
	}
	decoded, err := png.Decode(bytes.NewReader(picture))
	if err != nil {
		return nil, errors.New("a block must be a PNG")
	}
	nrgba := image.NewNRGBA(decoded.Bounds())
	draw.Draw(nrgba, nrgba.Bounds(), decoded, decoded.Bounds().Min, draw.Src)
	return nrgba, nil
}

// SaveWatermark stores the settings and the blocks' pictures: pictures[i]
// replaces block i's picture; nil keeps the saved picture of block i, which
// must exist. Blocks beyond the new count are deleted.
func SaveWatermark(settings WatermarkSettings, pictures [][]byte) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	if len(pictures) != len(settings.Blocks) {
		return errors.New("one picture entry per block")
	}
	watermarkMutex.Lock()
	defer watermarkMutex.Unlock()
	area := 0
	for i, picture := range pictures {
		if picture == nil {
			saved, err := os.ReadFile(watermarkBlockFile(i))
			if err != nil {
				return fmt.Errorf("block %d needs a picture", i+1)
			}
			picture = saved
		}
		decoded, err := DecodeWatermark(picture)
		if err != nil {
			return fmt.Errorf("block %d: %v", i+1, err)
		}
		area += decoded.Bounds().Dx() * decoded.Bounds().Dy()
	}
	if area > WatermarkMaxArea {
		return fmt.Errorf("the blocks together must be at most %d pixels (now %d)", WatermarkMaxArea, area)
	}
	if err := os.MkdirAll(WatermarkDir, 0o755); err != nil {
		return err
	}
	for i, picture := range pictures {
		if picture != nil {
			if err := writeFileAtomic(watermarkBlockFile(i), picture); err != nil {
				return err
			}
		}
	}
	for i := len(pictures); i < WatermarkMaxBlocks; i++ {
		_ = os.Remove(watermarkBlockFile(i))
	}
	raw, _ := json.Marshal(settings)
	return writeFileAtomic(watermarkSettingsFile(), append(raw, '\n'))
}

func writeFileAtomic(path string, data []byte) error {
	temporary := path + ".new"
	if err := os.WriteFile(temporary, data, 0o644); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

// appendARGB appends a picture as 0xAARRGGBB words.
func appendARGB(out []uint32, picture *image.NRGBA) []uint32 {
	bounds := picture.Bounds()
	for y := 0; y < bounds.Dy(); y++ {
		row := picture.Pix[y*picture.Stride:]
		for x := 0; x < bounds.Dx(); x++ {
			p := row[x*4 : x*4+4]
			out = append(out, uint32(p[3])<<24|uint32(p[0])<<16|uint32(p[1])<<8|uint32(p[2]))
		}
	}
	return out
}

// loadRegionDriver loads the region engine's driver, kept out at boot.
func loadRegionDriver() error {
	if _, err := os.Stat(rgnDevice); err == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, "modprobe", "cv181x_rgn").CombinedOutput(); err != nil {
		return fmt.Errorf("cannot load the region driver: %v: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// ApplyWatermark gives the saved watermark to the native library.
func ApplyWatermark() error {
	return applyWatermark(GetKvmVision())
}

func applyWatermark(vision *KvmVision) error {
	watermarkMutex.Lock()
	defer watermarkMutex.Unlock()
	settings, pictures := LoadWatermark()
	if !settings.Enabled || len(settings.Blocks) == 0 {
		vision.SetWatermark(nil, nil, 0)
		return nil
	}
	var argb []uint32
	geometry := make([]int32, 0, 4*len(settings.Blocks))
	for i, raw := range pictures {
		picture, err := DecodeWatermark(raw)
		if err != nil {
			vision.SetWatermark(nil, nil, 0)
			return fmt.Errorf("block %d: %v", i+1, err)
		}
		bounds := picture.Bounds()
		argb = appendARGB(argb, picture)
		geometry = append(geometry, int32(bounds.Dx()), int32(bounds.Dy()),
			int32(settings.Blocks[i].X), int32(settings.Blocks[i].Y))
	}
	if err := loadRegionDriver(); err != nil {
		return err
	}
	if shown := vision.SetWatermark(argb, geometry, settings.Margin); shown < 0 {
		return fmt.Errorf("the native library refused the watermark (%d)", shown)
	}
	log.Infof("watermark on: %d blocks, margin %d", len(settings.Blocks), settings.Margin)
	return nil
}
