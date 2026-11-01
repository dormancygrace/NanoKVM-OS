package vm

import (
	"NanoKVM-Server/pkg/bmc"
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type actionATX struct {
	mu            sync.Mutex
	on            bool
	presses       []int
	cycleReleased chan struct{}
}

func (a *actionATX) State(context.Context) (bmc.ATXState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return bmc.ATXState{PowerButton: true, ResetButton: true, PowerLED: true, PowerOn: a.on}, nil
}
func (a *actionATX) Press(_ context.Context, button string, ms int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.presses = append(a.presses, ms)
	if ms >= 4000 {
		a.on = false
		if a.cycleReleased != nil {
			close(a.cycleReleased)
			a.cycleReleased = nil
		}
	} else {
		a.on = !a.on
	}
	return nil
}

func TestInternalPowerActionValidation(t *testing.T) {
	for _, action := range []bmc.ATXAction{{Action: "bogus"}, {Action: "On", HoldSeconds: 31}, {Action: "On", HoldSeconds: -1}} {
		body, _ := json.Marshal(action)
		atx := &actionATX{}
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest("POST", "/api/internal/atx", strings.NewReader(string(body)))
		c.Request.Header.Set("Content-Type", "application/json")
		pressATXInternal(c, atx)
		if rec.Code != http.StatusBadRequest || len(atx.presses) != 0 {
			t.Fatalf("%s: %d %v", body, rec.Code, atx.presses)
		}
	}
}
