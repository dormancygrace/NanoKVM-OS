package vm

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestScreenTextCrop(t *testing.T) {
	for _, bad := range []string{"", "0.1,0.2,0.3", "a,0,1,1", "0,0,0,1", "-0.1,0,1,1", "0,0,1.5,1", "NaN,0,1,1"} {
		if _, err := parseScreenTextCrop(bad); err == nil {
			t.Errorf("parseScreenTextCrop(%q) accepted", bad)
		}
	}
	for _, tc := range []struct {
		crop string
		want ScreenTextArea
	}{
		// A quarter in the middle of Full HD, with the margin around it.
		{"0.25,0.25,0.5,0.5", ScreenTextArea{X: 480 - 24, Y: 270 - 24, W: 960 + 48, H: 540 + 48}},
		// At the corner the margin stops at the picture.
		{"0,0,0.1,0.1", ScreenTextArea{X: 0, Y: 0, W: 192 + 24, H: 108 + 24}},
		// A sliver grows to 32 pixels within the picture.
		{"1,1,0,0.0001", ScreenTextArea{}}, // empty: rejected above
		{"0.999,0.999,0.001,0.001", ScreenTextArea{X: 1888, Y: 1048, W: 32, H: 32}},
	} {
		f, err := parseScreenTextCrop(tc.crop)
		if tc.want == (ScreenTextArea{}) {
			if err == nil {
				t.Errorf("parseScreenTextCrop(%q) accepted", tc.crop)
			}
			continue
		}
		if err != nil {
			t.Fatalf("parseScreenTextCrop(%q): %v", tc.crop, err)
		}
		if got := screenTextCropArea(f, 1920, 1080); got != tc.want {
			t.Errorf("crop %q = %+v, want %+v", tc.crop, got, tc.want)
		}
	}
}

func TestCropPlanes(t *testing.T) {
	// 4x3 picture; every byte tells its plane and position.
	const width, height = 4, 3
	planes := make([]byte, 3*width*height)
	for c := range 3 {
		for i := range width * height {
			planes[c*width*height+i] = byte(c*100 + i)
		}
	}
	got := cropPlanes(planes, width, height, ScreenTextArea{X: 1, Y: 1, W: 2, H: 2})
	want := []byte{5, 6, 9, 10, 105, 106, 109, 110, 205, 206, 209, 210}
	if !slices.Equal(got, want) {
		t.Errorf("cropPlanes = %v, want %v", got, want)
	}
}

func TestParseScreenTextOutput(t *testing.T) {
	out := []byte(`{"text":"Привет \"x\"","score":0.987,"x":10,"y":20,"w":30,"h":12,"slope":-0.0125}
not json
{"done":true,"words":1,"reread":0,"det_ms":300,"rec_ms":20}
`)
	boxes, done := parseScreenTextOutput(out, 100, 200)
	want := ScreenTextBox{Text: `Привет "x"`, Score: 0.987, X: 110, Y: 220, W: 30, H: 12, Slope: -0.0125}
	if len(boxes) != 1 || boxes[0] != want {
		t.Errorf("boxes = %+v, want [%+v]", boxes, want)
	}
	if string(done) != `{"done":true,"words":1,"reread":0,"det_ms":300,"rec_ms":20}` {
		t.Errorf("summary = %s", done)
	}
	if boxes, _ := parseScreenTextOutput(nil, 0, 0); boxes == nil || len(boxes) != 0 {
		t.Errorf("no output: %v, want an empty list (JSON [])", boxes)
	}
}

func TestScreenTextLines(t *testing.T) {
	box := func(text string, x, y, w, h int) ScreenTextBox {
		return ScreenTextBox{Text: text, Score: 0.9, X: x, Y: y, W: w, H: h}
	}
	for _, tc := range []struct {
		name  string
		boxes []ScreenTextBox
		want  string
	}{
		{"reading order", []ScreenTextBox{box("world", 60, 10, 40, 16), box("second", 10, 40, 40, 16),
			box("Hello", 10, 12, 40, 16), box("line", 70, 41, 40, 16)}, "Hello world\nsecond line"},
		{"close lines apart", []ScreenTextBox{box("b", 10, 20, 40, 16), box("a", 10, 0, 40, 16)}, "a\nb"},
		{"tall box", []ScreenTextBox{box("Title", 0, 0, 200, 60), box("one", 300, 5, 40, 16), box("two", 300, 40, 40, 16)},
			"one\nTitle\ntwo"},
		// A photo of a kernel panic: boxes higher than the line pitch, lines rising.
		{"slanted small text", []ScreenTextBox{
			box("db_trace_self_wrapper()", 480, 518, 188, 20), box("at db_trace_sel", 668, 522, 129, 19),
			box("f_wrapper+0x2b/frame", 797, 524, 163, 20), box("0xfffffe00d783d640", 960, 528, 148, 20),
			box("anic() at vpanic+0x136/", 500, 528, 184, 21), box("frame 0xfffffe00d783d770", 684, 532, 194, 21),
			box("panic()", 484, 544, 60, 11), box("at panic+0x43/frame", 540, 544, 164, 18),
			box("0xfffffe00d783d7d0", 704, 547, 152, 17)},
			"db_trace_self_wrapper() at db_trace_sel f_wrapper+0x2b/frame 0xfffffe00d783d640\n" +
				"anic() at vpanic+0x136/ frame 0xfffffe00d783d770\n" +
				"panic() at panic+0x43/frame 0xfffffe00d783d7d0"},
		{"empty", nil, ""},
	} {
		if got := ScreenTextLines(tc.boxes); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestScreenTextDefaultLanguage(t *testing.T) {
	saved := screenTextLanguageFile
	defer func() { screenTextLanguageFile = saved }()
	screenTextLanguageFile = filepath.Join(t.TempDir(), "language")
	if got := screenTextDefaultLanguage(); got != "latin" {
		t.Errorf("without a file: %q, want latin", got)
	}
	for content, want := range map[string]string{"cyrillic\n": "cyrillic", "korean\n": "latin", "": "latin"} {
		if err := os.WriteFile(screenTextLanguageFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := screenTextDefaultLanguage(); got != want {
			t.Errorf("file %q: %q, want %q", content, got, want)
		}
	}
}

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
