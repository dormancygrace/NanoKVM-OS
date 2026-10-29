package kvmcapture

import (
	"context"
	"errors"

	mcpservice "NanoKVM-Server/service/mcp"
	"NanoKVM-Server/service/vm"
)

// textReader reads the screen text through the TPU add-on, as the web UI
// does (vm.ReadScreenText): one read at a time across all of them.
type textReader struct {
	read func(context.Context, vm.ScreenTextRequest) (*vm.ScreenTextReply, error)
}

func NewTextReader() mcpservice.ScreenTextReader {
	return textReader{read: vm.ReadScreenText}
}

func (textReader) Available() bool { return vm.ScreenTextAvailable() }

func (textReader) Languages() []string { return vm.ScreenTextLanguageNames() }

func (r textReader) ReadText(ctx context.Context, language string, area *mcpservice.ScreenBox) (mcpservice.ScreenText, error) {
	req := vm.ScreenTextRequest{Language: language}
	if area != nil {
		req.Crop = &[4]float64{area.X, area.Y, area.W, area.H}
	}
	reply, err := r.read(ctx, req)
	if err != nil {
		var readErr *vm.ScreenTextError
		if errors.As(err, &readErr) && readErr.Code == -1 {
			return mcpservice.ScreenText{}, mcpservice.ErrNoTextReader
		}
		return mcpservice.ScreenText{}, err
	}
	return screenText(reply), nil
}

// screenText is a reply in the MCP's terms: boxes normalized to the screen.
func screenText(reply *vm.ScreenTextReply) mcpservice.ScreenText {
	width, height := float64(reply.Width), float64(reply.Height)
	pieces := make([]mcpservice.ScreenTextPiece, 0, len(reply.Boxes))
	for _, box := range reply.Boxes {
		pieces = append(pieces, mcpservice.ScreenTextPiece{Text: box.Text, Score: box.Score, Box: mcpservice.ScreenBox{
			X: float64(box.X) / width, Y: float64(box.Y) / height,
			W: float64(box.W) / width, H: float64(box.H) / height,
		}})
	}
	return mcpservice.ScreenText{
		Language: reply.Language, Width: reply.Width, Height: reply.Height,
		Text: vm.ScreenTextLines(reply.Boxes), Pieces: pieces,
	}
}
