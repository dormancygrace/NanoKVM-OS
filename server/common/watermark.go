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
	"sync/atomic"
	"syscall"
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
	// PNG bytes of one block and of all blocks together. The saved file
	// (the pictures in base64, the design and the JSON around them) stays
	// within what a settings archive takes of one file (recovery.MaxFile,
	// 512 KiB).
	WatermarkMaxBytes      = 256 << 10
	WatermarkMaxTotalBytes = 360 << 10
	watermarkMaxFile       = 512 << 10
	// A request: the pictures in base64, the design and the JSON around them.
	WatermarkMaxRequest = WatermarkMaxTotalBytes/3*4 + 64<<10
	watermarkMaxMargin  = 200
	watermarkMaxDesign  = 8192
	rgnDevice           = "/dev/cvi-rgn"
)

// WatermarkFile holds the whole watermark, the blocks' PNGs included, so that
// a save replaces it in one rename (a failed save leaves the previous one)
// and settings backup, restore and reset handle it as one setting.
var (
	WatermarkFile  = "/etc/kvm/watermark.json"
	watermarkMutex sync.Mutex
)

// watermarkFile is WatermarkFile's content: the settings, and each block's
// picture beside its place.
type watermarkFile struct {
	WatermarkSettings
	Pictures [][]byte `json:"pictures"`
}

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

// LoadWatermark returns the saved settings and each block's picture (nil
// where missing).
func LoadWatermark() (WatermarkSettings, [][]byte) {
	watermarkMutex.Lock()
	defer watermarkMutex.Unlock()
	return loadWatermarkLocked()
}

func loadWatermarkLocked() (WatermarkSettings, [][]byte) {
	settings := DefaultWatermarkSettings()
	raw, err := os.ReadFile(WatermarkFile)
	if err != nil || len(raw) > watermarkMaxFile {
		return settings, [][]byte{}
	}
	var saved watermarkFile
	if json.Unmarshal(raw, &saved) != nil || saved.Validate() != nil {
		return settings, [][]byte{}
	}
	if saved.Blocks == nil {
		saved.Blocks = []WatermarkBlock{}
	}
	pictures := make([][]byte, len(saved.Blocks))
	copy(pictures, saved.Pictures)
	return saved.WatermarkSettings, pictures
}

// DecodeWatermark checks a block's PNG and returns its pixels.
func DecodeWatermark(picture []byte) (*image.NRGBA, error) {
	if len(picture) == 0 || len(picture) > WatermarkMaxBytes {
		return nil, fmt.Errorf("a block must be a PNG of at most %d KiB", WatermarkMaxBytes>>10)
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
// must exist. The file is replaced in one rename: nothing changes unless all
// of it is written.
func SaveWatermark(settings WatermarkSettings, pictures [][]byte) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	if len(pictures) != len(settings.Blocks) {
		return errors.New("one picture entry per block")
	}
	watermarkMutex.Lock()
	defer watermarkMutex.Unlock()
	_, saved := loadWatermarkLocked()
	complete := make([][]byte, len(pictures))
	area, total := 0, 0
	for i, picture := range pictures {
		if picture == nil {
			if i >= len(saved) || saved[i] == nil {
				return fmt.Errorf("block %d needs a picture", i+1)
			}
			picture = saved[i]
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
		return fmt.Errorf("the blocks together must be at most %d KiB of PNG", WatermarkMaxTotalBytes>>10)
	}
	raw, err := json.Marshal(watermarkFile{WatermarkSettings: settings, Pictures: complete})
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if len(raw) > watermarkMaxFile {
		return fmt.Errorf("the watermark must be at most %d KiB", watermarkMaxFile>>10)
	}
	return writeFileSynced(WatermarkFile, raw)
}

// writeFileSynced replaces path with data: a synced temporary file renamed
// over it, and the directory synced.
func writeFileSynced(path string, data []byte) error {
	temporary := path + ".new"
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temporary, path)
	}
	if err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
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

// watermarkUnapplied: the saved watermark is not the one the native library
// has, because giving it failed. Until it is given, or switched off, a
// picture of the screen made for people would lack it.
var watermarkUnapplied atomic.Bool

// WatermarkUnapplied reports that the saved watermark could not be given to
// the native library: pictures that must carry it are withheld.
func WatermarkUnapplied() bool { return watermarkUnapplied.Load() }

func applyWatermark(vision *KvmVision) error {
	watermarkMutex.Lock()
	defer watermarkMutex.Unlock()
	// Until this returns without an error the library may have another
	// watermark than the saved one, or none.
	watermarkUnapplied.Store(true)
	settings, pictures := loadWatermarkLocked()
	if !settings.Enabled || len(settings.Blocks) == 0 {
		vision.SetWatermark(nil, nil, 0)
		watermarkUnapplied.Store(false)
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
	shown := vision.SetWatermark(argb, geometry, settings.Margin)
	// -EIO: the library has the watermark, and a block is missing on an
	// output of a stream. A single picture puts the blocks on for itself and
	// is refused when one fails, so it need not be withheld for that.
	if shown >= 0 || shown == -int(syscall.EIO) {
		watermarkUnapplied.Store(false)
	}
	if shown < 0 {
		return fmt.Errorf("the native library refused the watermark (%d)", shown)
	}
	log.Infof("watermark on: %d blocks, margin %d", len(settings.Blocks), settings.Margin)
	return nil
}
