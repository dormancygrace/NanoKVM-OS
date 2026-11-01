package picoclaw

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"NanoKVM-Server/service/controlmode"

	"github.com/gin-gonic/gin"
)

func TestMCPActionsReturnStructuredControlErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, test := range []struct {
		mode   controlmode.Mode
		reason string
	}{
		{mode: controlmode.ModeOff, reason: CodeControlRequired},
		{mode: controlmode.ModeMCP, reason: CodeControlOwnedByMCP},
	} {
		t.Run(string(test.mode), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mode")
			if err := os.WriteFile(path, []byte(string(test.mode)+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			service := &Service{control: controlmode.NewManager(path, controlmode.ModePicoclaw)}
			response := performMCPRequest(
				service,
				`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"kvm_actions","arguments":{"actions":[]}}}`,
			)
			body := response.Body.String()
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, body)
			}
			if !strings.Contains(body, `"code":-32003`) || !strings.Contains(body, `"reason":"`+test.reason+`"`) {
				t.Fatalf("body = %s, want structured %s error", body, test.reason)
			}
		})
	}
}

func TestMCPActionsRejectedDuringControlTransition(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := controlmode.NewManager(filepath.Join(t.TempDir(), "mode"), controlmode.ModePicoclaw)
	service := &Service{control: manager}

	err := manager.SwitchWithCleanup(controlmode.ModeMCP, func() error {
		response := performMCPRequest(
			service,
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"kvm_actions","arguments":{"actions":[]}}}`,
		)
		body := response.Body.String()
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", response.Code, body)
		}
		if !strings.Contains(body, `"reason":"`+CodeControlTransitioning+`"`) {
			t.Fatalf("body = %s, want transitioning control error", body)
		}
		return nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func performMCPRequest(service *Service, body string) *httptest.ResponseRecorder {
	router := gin.New()
	router.POST("/", service.MCPHandler)

	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}
