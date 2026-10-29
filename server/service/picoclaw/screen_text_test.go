package picoclaw

import (
	"context"
	"strings"
	"testing"

	mcpservice "NanoKVM-Server/service/mcp"

	"github.com/gin-gonic/gin"
)

type screenText struct {
	language string
	area     *mcpservice.ScreenBox
	off      bool
}

func (r *screenText) Available() bool { return !r.off }

func (*screenText) Languages() []string { return []string{"latin", "chinese", "cyrillic"} }

func (r *screenText) ReadText(_ context.Context, language string, area *mcpservice.ScreenBox) (mcpservice.ScreenText, error) {
	r.language, r.area = language, area
	return mcpservice.ScreenText{Language: "cyrillic", Width: 1920, Height: 1080, Text: "Пуск",
		Pieces: []mcpservice.ScreenTextPiece{{Text: "Пуск", Score: 0.9, Box: mcpservice.ScreenBox{X: 0.01, Y: 0.96, W: 0.03, H: 0.02}}}}, nil
}

func toolCall(arguments string) string {
	return `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"kvm_screen_text","arguments":` + arguments + `}}`
}

func TestMCPScreenText(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &Service{}
	if list := performMCPRequest(service, jsonRPCBody("tools/list")).Body.String(); strings.Contains(list, `"kvm_screen_text"`) {
		t.Fatalf("tools/list without a reader offers kvm_screen_text: %s", list)
	}
	if body := performMCPRequest(service, toolCall(`{}`)).Body.String(); !strings.Contains(body, `"isError":true`) ||
		!strings.Contains(body, "TPU add-on") {
		t.Fatalf("without a reader: %s", body)
	}

	// Text recognition off: not offered.
	reader := &screenText{off: true}
	service.SetTextReader(reader)
	if list := performMCPRequest(service, jsonRPCBody("tools/list")).Body.String(); strings.Contains(list, `"kvm_screen_text"`) ||
		!strings.Contains(list, `"kvm_actions"`) {
		t.Fatalf("tools/list with text recognition off: %s", list)
	}
	reader.off = false
	if list := performMCPRequest(service, jsonRPCBody("tools/list")).Body.String(); !strings.Contains(list, `"kvm_screen_text"`) {
		t.Fatalf("tools/list: %s", list)
	}
	body := performMCPRequest(service, toolCall(`{"language":"cyrillic","area":{"x":0,"y":0.9,"w":0.2,"h":0.1}}`)).Body.String()
	if strings.Contains(body, `"isError":true`) || !strings.Contains(body, `Пуск`) || !strings.Contains(body, `\"ok\":true`) {
		t.Fatalf("read: %s", body)
	}
	if reader.language != "cyrillic" || reader.area == nil || *reader.area != (mcpservice.ScreenBox{X: 0, Y: 0.9, W: 0.2, H: 0.1}) {
		t.Fatalf("request: %q %+v", reader.language, reader.area)
	}
	if body := performMCPRequest(service, toolCall(`{"area":{"x":0.9,"y":0,"w":0.2,"h":0.1}}`)).Body.String(); !strings.Contains(body, `"isError":true`) {
		t.Fatalf("area past the screen: %s", body)
	}
}
