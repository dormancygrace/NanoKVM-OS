package kvmcapture

import (
	"context"
	"errors"
	"testing"

	mcpservice "NanoKVM-Server/service/mcp"
	"NanoKVM-Server/service/vm"
)

func TestTextReaderNormalizesAndCrops(t *testing.T) {
	var got vm.ScreenTextRequest
	reader := textReader{read: func(_ context.Context, req vm.ScreenTextRequest) (*vm.ScreenTextReply, error) {
		got = req
		return &vm.ScreenTextReply{Language: "cyrillic", Width: 1920, Height: 1080, Boxes: []vm.ScreenTextBox{
			{Text: "мир", Score: 0.9, X: 960, Y: 540, W: 96, H: 27},
			{Text: "Привет,", Score: 0.95, X: 800, Y: 540, W: 150, H: 27},
		}}, nil
	}}
	text, err := reader.ReadText(context.Background(), "cyrillic", &mcpservice.ScreenBox{X: 0.25, Y: 0.25, W: 0.5, H: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	if got.Language != "cyrillic" || got.Crop == nil || *got.Crop != [4]float64{0.25, 0.25, 0.5, 0.5} || got.SavePicture {
		t.Errorf("request %+v", got)
	}
	if text.Text != "Привет, мир" || len(text.Pieces) != 2 || text.Language != "cyrillic" {
		t.Errorf("text %+v", text)
	}
	if box := text.Pieces[0].Box; box != (mcpservice.ScreenBox{X: 0.5, Y: 0.5, W: 0.05, H: 0.025}) {
		t.Errorf("box %+v", box)
	}
}

func TestTextReaderWithoutTheAddon(t *testing.T) {
	reader := textReader{read: func(context.Context, vm.ScreenTextRequest) (*vm.ScreenTextReply, error) {
		return nil, &vm.ScreenTextError{Code: -1, Message: "the TPU add-on is not installed"}
	}}
	if _, err := reader.ReadText(context.Background(), "", nil); !errors.Is(err, mcpservice.ErrNoTextReader) {
		t.Errorf("error %v, want ErrNoTextReader", err)
	}
	busy := textReader{read: func(context.Context, vm.ScreenTextRequest) (*vm.ScreenTextReply, error) {
		return nil, &vm.ScreenTextError{Code: -2, Message: "the screen is being read"}
	}}
	if _, err := busy.ReadText(context.Background(), "", nil); err == nil || err.Error() != "the screen is being read" {
		t.Errorf("busy: %v", err)
	}
}
