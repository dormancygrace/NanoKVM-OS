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
	// PNG bytes of one block and of all blocks together. A block of at
	// most WatermarkMaxArea pixels stays below them even uncompressed.
	WatermarkMaxBytes      = 1 << 20
	WatermarkMaxTotalBytes = 2 << 20
	// A request: the pictures in base64, the design and the JSON around them.
	WatermarkMaxRequest = WatermarkMaxTotalBytes/3*4 + 64<<10
	watermarkMaxMargin  = 200
	watermarkMaxDesign  = 8192
	rgnDevice           = "/dev/cvi-rgn"
)

// WatermarkDir is a symbolic link to the saved generation, a directory in
// WatermarkDir+".d" with settings.json and block-N.png. A save writes a whole
// new generation and then replaces the link in one rename, so a failed save
// leaves the previous watermark as it was.
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
	watermarkMutex.Lock()
	defer watermarkMutex.Unlock()
	return loadWatermarkLocked()
}

func loadWatermarkLocked() (WatermarkSettings, [][]byte) {
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
	if len(picture) == 0 || len(picture) > WatermarkMaxBytes {
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

// SaveWatermark stores the settings and the blocks' pictures as a new
// generation: pictures[i] replaces block i's picture; nil keeps the saved
// picture of block i, which must exist. Nothing changes unless all of it is
// written.
func SaveWatermark(settings WatermarkSettings, pictures [][]byte) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	if len(pictures) != len(settings.Blocks) {
		return errors.New("one picture entry per block")
	}
	watermarkMutex.Lock()
	defer watermarkMutex.Unlock()
	complete := make([][]byte, len(pictures))
	area, total := 0, 0
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
		total += len(picture)
		complete[i] = picture
	}
	if area > WatermarkMaxArea {
		return fmt.Errorf("the blocks together must be at most %d pixels (now %d)", WatermarkMaxArea, area)
	}
	if total > WatermarkMaxTotalBytes {
		return fmt.Errorf("the blocks together must be at most %d bytes of PNG", WatermarkMaxTotalBytes)
	}
	generations := WatermarkDir + ".d"
	if err := os.MkdirAll(generations, 0o755); err != nil {
		return err
	}
	generation, err := os.MkdirTemp(generations, "g")
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(generation)
		}
	}()
	if err := os.Chmod(generation, 0o755); err != nil {
		return err
	}
	for i, picture := range complete {
		if err := os.WriteFile(filepath.Join(generation, fmt.Sprintf("block-%d.png", i)), picture, 0o644); err != nil {
			return err
		}
	}
	raw, _ := json.Marshal(settings)
	if err := os.WriteFile(filepath.Join(generation, "settings.json"), append(raw, '\n'), 0o644); err != nil {
		return err
	}
	if err := syncDir(generation); err != nil {
		return err
	}
	// The link replaces the previous one (or a directory of an earlier
	// layout, which is removed first) in one rename.
	if info, err := os.Lstat(WatermarkDir); err == nil && info.IsDir() {
		if err := os.RemoveAll(WatermarkDir); err != nil {
			return err
		}
	}
	link := WatermarkDir + ".new"
	_ = os.Remove(link)
	if err := os.Symlink(generation, link); err != nil {
		return err
	}
	if err := os.Rename(link, WatermarkDir); err != nil {
		_ = os.Remove(link)
		return err
	}
	committed = true
	_ = syncDir(filepath.Dir(WatermarkDir))
	// Earlier generations are no longer referenced.
	if entries, err := os.ReadDir(generations); err == nil {
		for _, entry := range entries {
			if old := filepath.Join(generations, entry.Name()); old != generation {
				_ = os.RemoveAll(old)
			}
		}
	}
	return nil
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
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
	settings, pictures := loadWatermarkLocked()
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
