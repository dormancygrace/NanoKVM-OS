package vm

import (
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"NanoKVM-Server/proto"
)

// The built-in layouts kvm_system publishes, read from its source.
func builtInLayouts(t *testing.T) []string {
	source, err := os.ReadFile("../../../support/kvm_system/main/lib/oled_layout/oled_layout.cpp")
	if err != nil {
		t.Fatal(err)
	}
	var layouts []string
	for _, block := range regexp.MustCompile(`(?s)const char \w+_default\[\] =\n(.*?);`).FindAllStringSubmatch(string(source), -1) {
		var text strings.Builder
		for _, literal := range regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`).FindAllStringSubmatch(block[1], -1) {
			value, err := strconv.Unquote(`"` + literal[1] + `"`)
			if err != nil {
				t.Fatal(err)
			}
			text.WriteString(value)
		}
		layouts = append(layouts, text.String())
	}
	if len(layouts) != 2 {
		t.Fatalf("found %d built-in layouts", len(layouts))
	}
	return layouts
}

func TestOLEDLayoutRoundTrip(t *testing.T) {
	for _, text := range builtInLayouts(t) {
		layout, err := parseOLEDLayout(text)
		if err != nil {
			t.Fatalf("built-in layout: %v", err)
		}
		compiled, err := compileOLEDLayout(layout)
		if err != nil || compiled != text {
			t.Fatalf("compiled differently (%v):\n%s\nwant:\n%s", err, compiled, text)
		}
	}
	layout := &proto.OLEDLayout{Panel: "wide", Rotation: 180, Burnin: "shift", Pages: 3, PageSeconds: 7, Items: []proto.OLEDItem{
		{Type: "text", X: -3, Y: 2, Font: 4, Align: "right", Width: 40, Off: true, When: "!hdmi,atx,page3", Text: "  a {viewers:3} b "},
		{Type: "text", X: 0, Y: 0, Font: 8, Align: "left"},
		{Type: "line", X: 0, Y: 0, X2: 127, Y2: 63, When: "stream"},
		{Type: "rect", X: 1, Y: 2, Width: 3, Height: 4, Fill: true},
		{Type: "image", X: 5, Y: 6, Width: 9, Height: 2, Data: "80804000"},
	}}
	compiled, err := compileOLEDLayout(layout)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseOLEDLayout(compiled)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parsed, layout) {
		t.Fatalf("round trip:\n%+v\nwant\n%+v", parsed, layout)
	}
}

func TestOLEDLayoutRefusesWhatKvmSystemWould(t *testing.T) {
	item := func(item proto.OLEDItem) *proto.OLEDLayout {
		return &proto.OLEDLayout{Panel: "narrow", Items: []proto.OLEDItem{item}}
	}
	bad := map[string]*proto.OLEDLayout{
		"panel":      {Panel: "huge"},
		"rotation":   {Panel: "wide", Rotation: 45},
		"burn-in":    {Panel: "wide", Burnin: "roam", RoamWidth: 0, RoamHeight: 8},
		"font":       item(proto.OLEDItem{Type: "text", Font: 12, Align: "left"}),
		"align":      item(proto.OLEDItem{Type: "text", Font: 8, Align: "middle"}),
		"newline":    item(proto.OLEDItem{Type: "text", Font: 8, Align: "left", Text: "a\nimage 0 0 1 1 1 - 80"}),
		"non-ascii":  item(proto.OLEDItem{Type: "text", Font: 8, Align: "left", Text: "привет"}),
		"long":       item(proto.OLEDItem{Type: "text", Font: 8, Align: "left", Text: strings.Repeat("x", 129)}),
		"width":      item(proto.OLEDItem{Type: "text", Font: 8, Align: "left", Text: "{fps:1000000000}"}),
		"min int":    item(proto.OLEDItem{Type: "text", Font: 8, Align: "left", Text: "{fps:-2147483648}"}),
		"33":         item(proto.OLEDItem{Type: "text", Font: 8, Align: "left", Text: "{ip:-15}{fps:33}"}),
		"when":       item(proto.OLEDItem{Type: "line", When: "ethernet"}),
		"5 when":     item(proto.OLEDItem{Type: "line", When: "eth,wifi,hdmi,hid,stream"}),
		"empty when": item(proto.OLEDItem{Type: "line", When: "eth,"}),
		"pages":      {Panel: "wide", Pages: 5, PageSeconds: 5},
		"seconds":    {Panel: "wide", Pages: 2, PageSeconds: 0},
		"far":        item(proto.OLEDItem{Type: "line", X2: 999}),
		"size":       item(proto.OLEDItem{Type: "rect", Width: 0, Height: 4}),
		"image":      item(proto.OLEDItem{Type: "image", Width: 9, Height: 2, Data: "808040"}),
		"hex":        item(proto.OLEDItem{Type: "image", Width: 8, Height: 1, Data: "zz"}),
		"type":       item(proto.OLEDItem{Type: "circle"}),
	}
	many := &proto.OLEDLayout{Panel: "wide"}
	for i := 0; i <= oledMaxItems; i++ {
		many.Items = append(many.Items, proto.OLEDItem{Type: "line"})
	}
	bad["items"] = many
	for name, layout := range bad {
		if _, err := compileOLEDLayout(layout); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := parseOLEDLayout("nanokvm-oled 1 wide\ntext 0 0 8 left 0 1 ethernet x\n"); err == nil {
		t.Error("parsed an unknown condition")
	}
}
