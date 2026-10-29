package mcpservice

import (
	"context"
	"errors"

	"github.com/google/jsonschema-go/jsonschema"
	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
)

const screenTextToolName = "screen_text"

// ErrNoTextReader: the TPU add-on, which reads the text, is not installed.
var ErrNoTextReader = errors.New("reading the screen text needs the TPU add-on")

// ScreenBox is a part of the screen in normalized [0,1] coordinates, the
// frame of the absolute mouse tools.
type ScreenBox struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// ScreenTextPiece is a piece of a line the recognizer read, with its box.
type ScreenTextPiece struct {
	Text  string    `json:"text"`
	Score float64   `json:"score"`
	Box   ScreenBox `json:"box"`
}

// ScreenText is the text on the screen: Text has the lines in reading order,
// Pieces their parts with boxes (to click on a word: the middle of its box).
type ScreenText struct {
	Language string            `json:"language"`
	Width    int               `json:"width"`
	Height   int               `json:"height"`
	Text     string            `json:"text"`
	Pieces   []ScreenTextPiece `json:"pieces"`
}

// ScreenTextReader reads the text on the screen; language "" is the first
// language (latin), area nil the whole screen. Available: the add-on is
// installed and text recognition is on, so the tool is offered.
type ScreenTextReader interface {
	Available() bool
	Languages() []string
	ReadText(ctx context.Context, language string, area *ScreenBox) (ScreenText, error)
}

type ScreenTextParams struct {
	Language string     `json:"language,omitempty"`
	Area     *ScreenBox `json:"area,omitempty"`
}

type ScreenTextResult struct {
	OK bool `json:"ok"`
	ScreenText
}

func screenTextHandler(reader ScreenTextReader) protocol.ToolHandlerFor[ScreenTextParams, ScreenTextResult] {
	return func(ctx context.Context, _ *protocol.CallToolRequest, input ScreenTextParams) (*protocol.CallToolResult, ScreenTextResult, error) {
		if reader == nil {
			return toolError(ErrNoTextReader.Error()), ScreenTextResult{}, nil
		}
		if area := input.Area; area != nil && (area.W <= 0 || area.H <= 0 || area.X+area.W > 1 || area.Y+area.H > 1) {
			return toolError("area must be non-empty and lie within the screen"), ScreenTextResult{}, nil
		}
		// Reading is no input: like the screenshot, it needs no control lease.
		text, err := reader.ReadText(ctx, input.Language, input.Area)
		if err != nil {
			return toolError(err.Error()), ScreenTextResult{}, nil
		}
		return nil, ScreenTextResult{OK: true, ScreenText: text}, nil
	}
}

func screenTextSchema(reader ScreenTextReader) *jsonschema.Schema {
	language := &jsonschema.Schema{
		Type: "string",
		Description: "Recognizer: latin (English and European languages), chinese (Simplified and " +
			"Traditional Chinese, Japanese, English) or cyrillic (Russian, Ukrainian and others, with English); " +
			"omitted, latin",
	}
	if reader != nil {
		for _, name := range reader.Languages() {
			language.Enum = append(language.Enum, name)
		}
	}
	area := objectSchema(map[string]*jsonschema.Schema{
		"x": numberSchema("Left edge, normalized", 0, 1),
		"y": numberSchema("Top edge, normalized", 0, 1),
		"w": numberSchema("Width, normalized", 0, 1),
		"h": numberSchema("Height, normalized", 0, 1),
	}, "x", "y", "w", "h")
	area.Description = "Read only this part of the screen (normalized, as the mouse tools): it is read at full resolution, so small text is found"
	return objectSchema(map[string]*jsonschema.Schema{"language": language, "area": area})
}
