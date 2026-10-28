package vm

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestScreenTextArgs(t *testing.T) {
	models := t.TempDir() + "/"
	for _, f := range []string{"latin/ocr-rec.cvimodel", "latin/ocr-sure.cvimodel", "cyrillic/ocr-rec.cvimodel"} {
		if err := os.MkdirAll(filepath.Dir(models+f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(models+f, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		lang string
		want []string // nil: an error
	}{
		{"", []string{"ocr-det.cvimodel", "latin/ocr-rec.cvimodel:3", "latin/ocr-dict.txt", "latin/ocr-sure.cvimodel:3"}},
		{"cyrillic", []string{"ocr-det.cvimodel", "cyrillic/ocr-rec.cvimodel:3", "cyrillic/ocr-dict.txt"}},
		{"korean", nil}, // not a language of the add-on
		{"../latin", nil},
	} {
		args, err := screenTextArgs(models, tc.lang)
		var want []string
		for _, w := range tc.want {
			want = append(want, models+w)
		}
		if (err != nil) != (tc.want == nil) || !slices.Equal(args, want) {
			t.Errorf("screenTextArgs(%q) = %q, %v; want %q", tc.lang, args, err, want)
		}
	}
	// A language of the add-on whose models are not installed.
	if args, err := screenTextArgs(t.TempDir()+"/", "cyrillic"); err == nil {
		t.Errorf("screenTextArgs without models = %q, want an error", args)
	}
}

func TestScreenTextSizeUpToFullHD(t *testing.T) {
	for _, tc := range []struct{ inW, inH, w, h int }{
		{1920, 1080, 1920, 1080},
		{1080, 1920, 1080, 1920},
		{1366, 768, 1366, 768},
		{1365, 767, 1364, 766},
		{2560, 1440, 0, 0},
		{3840, 2160, 0, 0},
		{1920, 1200, 0, 0},
	} {
		w, h := screenTextSize(tc.inW, tc.inH)
		if w != tc.w || h != tc.h {
			t.Errorf("screenTextSize(%d, %d) = %dx%d, want %dx%d", tc.inW, tc.inH, w, h, tc.w, tc.h)
		}
	}
}
