package branding

import (
	"bytes"

	"image"
	"image/png"

	"testing"
)

func TestOversizedDimensions(t *testing.T) {
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 1025, 1)))
	if _, e := normalizeLogo(b.Bytes()); e == nil {
		t.Fatal("oversized dimensions accepted")
	}
}
