package mcpservice

import (
	"context"
	"fmt"
	"strings"
	"time"

	"NanoKVM-Server/service/inputcontrol"

	"github.com/google/jsonschema-go/jsonschema"
	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	hostStatusToolName = "host_status"
	dragMouseToolName  = "drag_mouse"

	defaultDragSteps      = 20
	maxDragSteps          = 200
	defaultDragDurationMS = 300
	maxDragDurationMS     = 5000
)

// HostState is the host as the NanoKVM sees it without a screenshot: its
// power, the HDMI input and the keyboard LEDs the host sets.
type HostState struct {
	Power    HostPower    `json:"power"`
	Video    HostVideo    `json:"video"`
	Keyboard HostKeyboard `json:"keyboard"`
}

type HostPower struct {
	// The board can press the host's power and reset buttons.
	PowerButton bool `json:"powerButton"`
	ResetButton bool `json:"resetButton"`
	// A power LED is wired and trusted: only then does on mean anything.
	Known        bool   `json:"known"`
	On           bool   `json:"on"`
	DiskActivity bool   `json:"diskActivity"`
	Error        string `json:"error,omitempty"`
}

type HostVideo struct {
	// HDMI capture is on, and the input has a signal.
	Capture bool `json:"capture"`
	Signal  bool `json:"signal"`
}

type HostKeyboard struct {
	// The host has sent its LED state since the keyboard was attached.
	Known      bool `json:"known"`
	CapsLock   bool `json:"capsLock"`
	NumLock    bool `json:"numLock"`
	ScrollLock bool `json:"scrollLock"`
}

// HostStatusReader reads HostState; it never presses or changes anything.
type HostStatusReader interface {
	HostStatus(ctx context.Context) HostState
}

type HostStatusParams struct{}

type DragPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type DragMouseParams struct {
	Button     string     `json:"button,omitempty"`
	From       *DragPoint `json:"from"`
	To         *DragPoint `json:"to"`
	Steps      int        `json:"steps,omitempty"`
	DurationMS *int       `json:"durationMs,omitempty"`
}

// A read: no input lease, so it never takes the keyboard from a browser user.
func hostStatusHandler(reader HostStatusReader) protocol.ToolHandlerFor[HostStatusParams, HostState] {
	return func(ctx context.Context, _ *protocol.CallToolRequest, _ HostStatusParams) (*protocol.CallToolResult, HostState, error) {
		if reader == nil {
			return toolError("host status is unavailable"), HostState{}, nil
		}
		return nil, reader.HostStatus(ctx), nil
	}
}

func dragMouseHandler(executor *toolExecutor, remote *Remote) protocol.ToolHandlerFor[DragMouseParams, ToolOKResult] {
	return func(ctx context.Context, _ *protocol.CallToolRequest, input DragMouseParams) (*protocol.CallToolResult, ToolOKResult, error) {
		button := strings.ToLower(strings.TrimSpace(input.Button))
		if button == "" {
			button = "left"
		}
		if input.From == nil || input.To == nil {
			return nil, ToolOKResult{}, fmt.Errorf("from and to are required")
		}
		steps := input.Steps
		if steps == 0 {
			steps = defaultDragSteps
		}
		if steps < 1 || steps > maxDragSteps {
			return nil, ToolOKResult{}, fmt.Errorf("steps must be between 1 and %d", maxDragSteps)
		}
		durationMS := defaultDragDurationMS
		if input.DurationMS != nil {
			durationMS = clampInt(*input.DurationMS, 0, maxDragDurationMS)
		}
		operationCtx, release, err := executor.begin(ctx, inputcontrol.OperationHID)
		if err != nil {
			return nil, ToolOKResult{}, err
		}
		defer release()
		from := [2]float64{input.From.X, input.From.Y}
		to := [2]float64{input.To.X, input.To.Y}
		if err := remote.Drag(operationCtx, button, from, to, steps, time.Duration(durationMS)*time.Millisecond); err != nil {
			return nil, ToolOKResult{}, err
		}
		return nil, ToolOKResult{OK: true}, nil
	}
}

func hostStatusSchema() *jsonschema.Schema {
	return objectSchema(map[string]*jsonschema.Schema{})
}

func dragMouseSchema() *jsonschema.Schema {
	point := func(description string) *jsonschema.Schema {
		schema := objectSchema(map[string]*jsonschema.Schema{
			"x": numberSchema("Normalized x coordinate", 0, 1),
			"y": numberSchema("Normalized y coordinate", 0, 1),
		}, "x", "y")
		schema.Description = description
		return schema
	}
	return objectSchema(map[string]*jsonschema.Schema{
		"button":     {Type: "string", Enum: []any{"left", "right", "middle"}},
		"from":       point("Where the button goes down (normalized, as the mouse tools)"),
		"to":         point("Where it is released"),
		"steps":      integerSchema(fmt.Sprintf("Moves between the two points; defaults to %d", defaultDragSteps), 1, maxDragSteps),
		"durationMs": integerSchema(fmt.Sprintf("Time of the move in milliseconds; defaults to %d", defaultDragDurationMS), 0, maxDragDurationMS),
	}, "from", "to")
}
