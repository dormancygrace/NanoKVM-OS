package vm

import (
	"NanoKVM-Server/internal/bmc"
	"context"
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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

// Real loopback Client -> production HTTP action handler -> shared Power.
// While IPMI's cycle waits, even a different Redfish controller must be busy.
func TestIPMICycleExcludesRedfishDuringSettle(t *testing.T) {
	released := make(chan struct{})
	atx := &actionATX{on: true, cycleReleased: released}
	engine := gin.New()
	engine.POST("/api/internal/atx", func(c *gin.Context) { pressATXInternal(c, atx) })
	server := httptest.NewServer(engine)
	defer server.Close()
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("fixture-token"), 0600); err != nil {
		t.Fatal(err)
	}
	client := &bmc.Client{Addr: strings.TrimPrefix(server.URL, "http://"), TokenFile: token, Timeout: time.Second, Hold: 6 * time.Second}
	done := make(chan error, 1)
	go func() { done <- client.Cycle(context.Background()) }()
	select {
	case <-released:
	case err := <-done:
		t.Fatalf("cycle returned before pressing: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("cycle did not start")
	}
	redfish := &bmc.Power{ATX: atx}
	if err := redfish.On(context.Background()); !errors.Is(err, bmc.ErrActionBusy) {
		t.Errorf("overlap: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	atx.mu.Lock()
	defer atx.mu.Unlock()
	if !atx.on || len(atx.presses) != 2 || atx.presses[0] != 6000 || atx.presses[1] != 800 {
		t.Fatalf("on=%v presses=%v", atx.on, atx.presses)
	}
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

// A real HTTP 503 must reach the daemon as busy, not a successful command.
func TestIPMIClientBusy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var action bmc.ATXAction
		if err := json.NewDecoder(r.Body).Decode(&action); err != nil || action.Action != "On" {
			t.Errorf("action %+v: %v", action, err)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("fixture-token"), 0600); err != nil {
		t.Fatal(err)
	}
	client := &bmc.Client{Addr: strings.TrimPrefix(server.URL, "http://"), TokenFile: token, Timeout: time.Second}
	if err := client.On(context.Background()); !errors.Is(err, bmc.ErrActionBusy) {
		t.Fatalf("busy: %v", err)
	}
}
