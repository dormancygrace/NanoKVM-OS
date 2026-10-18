package vm

import (
	"NanoKVM-Server/authn"
	"NanoKVM-Server/common"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/gin-gonic/gin"
)

// switchStand fakes a device for SetVideoSettings with fitVideoMemory: boot
// images, the booted mode and pool, the saved monitor, activate-kernel, the
// deferred monitor save and the restart.
type switchStand struct {
	saved       common.MonitorState
	activated   []string
	activateErr error
	deferred    []common.MonitorSettings
	restored    int
	programmed  []common.MonitorSettings
	programErr  error
	held        int
	restarts    int
}

func newSwitchStand(t *testing.T, active string, savedHeight uint16) *switchStand {
	t.Helper()
	root, put := videoMemoryRoot(t)
	putImages(put, "pcie", "pcie-qhd", "pcie-uhd")
	put(dtBase+"nanokvm,video-memory-mode", active+"\x00")
	pool, _ := videoMemoryPool(active)
	stand := &switchStand{saved: common.MonitorState{Resolution: savedHeight, PortraitResolution: 1920}}

	oldFiles, oldLock, oldRun := videoMemoryFiles, lockVideoMemory, runActivateKernel
	oldApply, oldSave, oldHold, oldState, oldPool, oldRestart := applyVideoMonitorSettings, saveMonitorForRestart,
		holdMonitorForRestart, monitorStateAfter, bootedVideoPool, restartDevice
	oldScreen := map[string]string{}
	for key, path := range screenFileMap {
		oldScreen[key] = path
		screenFileMap[key] = filepath.Join(t.TempDir(), key)
	}
	previous := *common.GetScreen()
	t.Cleanup(func() {
		videoMemoryFiles, lockVideoMemory, runActivateKernel = oldFiles, oldLock, oldRun
		applyVideoMonitorSettings, saveMonitorForRestart, holdMonitorForRestart = oldApply, oldSave, oldHold
		monitorStateAfter, bootedVideoPool, restartDevice = oldState, oldPool, oldRestart
		for key, path := range oldScreen {
			screenFileMap[key] = path
		}
		common.SetScreen("fps", previous.FPS)
		videoMemoryRestartPending.Store(false)
	})

	videoMemoryFiles = root
	lockVideoMemory = func() (*os.File, error) { return os.Open(os.DevNull) }
	runActivateKernel = func(_ context.Context, board, mode string) ([]byte, error) {
		if board != "pcie" {
			t.Errorf("board %q", board)
		}
		stand.activated = append(stand.activated, mode)
		if stand.activateErr != nil && len(stand.activated) == 1 {
			return []byte("activate-kernel: no space left"), stand.activateErr
		}
		return nil, nil
	}
	bootedVideoPool = func() common.VideoPool { return pool }
	monitorStateAfter = func(update common.MonitorSettings) common.MonitorState {
		state := stand.saved
		if update.Resolution != nil {
			state.Resolution = *update.Resolution
		}
		if update.Portrait != nil {
			state.Portrait = *update.Portrait
		}
		if update.PortraitResolution != nil {
			state.PortraitResolution = *update.PortraitResolution
		}
		return state
	}
	saveMonitorForRestart = func(update common.MonitorSettings, target common.VideoPool) (func() error, error) {
		stand.deferred = append(stand.deferred, update)
		return func() error { stand.restored++; return nil }, nil
	}
	applyVideoMonitorSettings = func(update common.MonitorSettings) error {
		stand.programmed = append(stand.programmed, update)
		return stand.programErr
	}
	holdMonitorForRestart = func() { stand.held++ }
	restartDevice = func(*gin.Context) { stand.restarts++ }
	return stand
}

type switchResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Restart     bool   `json:"restart"`
		VideoMemory string `json:"videoMemoryMode"`
	} `json:"data"`
}

func postVideo(t *testing.T, body string) switchResponse {
	t.Helper()
	_, raw := setVideoAs(t, body, authn.RoleAdmin)
	var rsp switchResponse
	if err := json.Unmarshal([]byte(raw), &rsp); err != nil {
		t.Fatalf("response %q: %v", raw, err)
	}
	return rsp
}

func TestVideoMemoryTargetIsTheSmallestFittingMode(t *testing.T) {
	all := []string{"fhd", "qhd", "uhd"}
	for _, tc := range []struct {
		installed []string
		state     common.MonitorState
		want      string
	}{
		{all, common.MonitorState{Resolution: 1080}, "fhd"},
		{all, common.MonitorState{Resolution: 0}, "fhd"},
		{all, common.MonitorState{Resolution: 1440}, "qhd"},
		{all, common.MonitorState{Resolution: 2160}, "uhd"},
		{all, common.MonitorState{Resolution: 1080, Portrait: true, PortraitResolution: 1920}, "qhd"},
		{[]string{"fhd", "uhd"}, common.MonitorState{Resolution: 1440}, "uhd"},
		{[]string{"qhd", "uhd"}, common.MonitorState{Resolution: 720}, "qhd"},
		{[]string{"fhd", "qhd"}, common.MonitorState{Resolution: 2160}, ""},
	} {
		got, ok := targetVideoMemoryMode(tc.installed, tc.state)
		if got != tc.want || ok != (tc.want != "") {
			t.Errorf("%v %+v: %q %v, want %q", tc.installed, tc.state, got, ok, tc.want)
		}
	}
}

func TestVideoSettingsSwitchUpDefersTheMonitor(t *testing.T) {
	for _, tc := range []struct {
		monitor, mode string
	}{{"1440", "qhd"}, {"2160", "uhd"}} {
		t.Run(tc.mode, func(t *testing.T) {
			stand := newSwitchStand(t, "fhd", 1080)
			rsp := postVideo(t, `{"fps":60,"monitor":`+tc.monitor+`,"fitVideoMemory":true}`)
			if rsp.Code != 0 || !rsp.Data.Restart || rsp.Data.VideoMemory != tc.mode {
				t.Fatalf("response %+v", rsp)
			}
			if !slices.Equal(stand.activated, []string{tc.mode}) || len(stand.deferred) != 1 ||
				len(stand.programmed) != 0 || stand.restored != 0 || stand.restarts != 1 {
				t.Fatalf("stand %+v", stand)
			}
			if value, _ := os.ReadFile(screenFileMap["fps"]); string(value) != "60" {
				t.Fatalf("stream rate %q not saved", value)
			}
			if !videoMemoryRestartPending.Load() {
				t.Fatal("accepts changes while restarting")
			}
			if again := postVideo(t, `{"fps":30}`); again.Code == 0 || stand.restarts != 1 {
				t.Fatalf("a change during the restart: %+v", again)
			}
		})
	}
}

func TestVideoSettingsSwitchDownProgramsNow(t *testing.T) {
	stand := newSwitchStand(t, "qhd", 1440)
	rsp := postVideo(t, `{"monitor":1080,"fitVideoMemory":true}`)
	if rsp.Code != 0 || !rsp.Data.Restart || rsp.Data.VideoMemory != "fhd" {
		t.Fatalf("response %+v", rsp)
	}
	if !slices.Equal(stand.activated, []string{"fhd"}) || len(stand.deferred) != 0 ||
		len(stand.programmed) != 1 || stand.held != 1 || stand.restarts != 1 {
		t.Fatalf("stand %+v", stand)
	}
}

func TestVideoSettingsSameModeDoesNotRestart(t *testing.T) {
	for _, tc := range []struct {
		name, active, body string
		saved              uint16
	}{
		{"the booted mode fits", "qhd", `{"monitor":1440,"fitVideoMemory":true}`, 1080},
		// A stream change alone never switches, even where a smaller mode fits.
		{"no monitor change", "qhd", `{"fps":30,"fitVideoMemory":true}`, 1080},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stand := newSwitchStand(t, tc.active, tc.saved)
			rsp := postVideo(t, tc.body)
			if rsp.Code != 0 || rsp.Data.Restart {
				t.Fatalf("response %+v", rsp)
			}
			if len(stand.activated) != 0 || len(stand.deferred) != 0 || len(stand.programmed) != 1 || stand.restarts != 0 {
				t.Fatalf("stand %+v", stand)
			}
		})
	}
}

func TestVideoSettingsWithoutTheFlagKeepTheBootedMode(t *testing.T) {
	stand := newSwitchStand(t, "fhd", 1080)
	if rsp := postVideo(t, `{"monitor":1440}`); rsp.Code != -3 {
		t.Fatalf("response %+v", rsp)
	}
	stand2 := newSwitchStand(t, "qhd", 1440)
	if rsp := postVideo(t, `{"monitor":1080}`); rsp.Code != 0 || rsp.Data.Restart {
		t.Fatalf("response %+v", rsp)
	}
	for _, s := range []*switchStand{stand, stand2} {
		if len(s.activated) != 0 || len(s.deferred) != 0 || s.restarts != 0 {
			t.Fatalf("stand %+v", s)
		}
	}
	if len(stand.programmed) != 0 || len(stand2.programmed) != 1 {
		t.Fatalf("programmed %v / %v", stand.programmed, stand2.programmed)
	}
}

func TestVideoSettingsSwitchRollsBackAFailedActivation(t *testing.T) {
	stand := newSwitchStand(t, "fhd", 1080)
	stand.activateErr = errors.New("exit status 1")
	rsp := postVideo(t, `{"fps":30,"monitor":1440,"fitVideoMemory":true}`)
	if rsp.Code != -2 || rsp.Data.Restart || !bytes.Contains([]byte(rsp.Msg), []byte("no space left")) {
		t.Fatalf("response %+v", rsp)
	}
	if stand.restored != 1 || stand.restarts != 0 || len(stand.programmed) != 0 || videoMemoryRestartPending.Load() {
		t.Fatalf("stand %+v", stand)
	}
	if _, err := os.Stat(screenFileMap["fps"]); !os.IsNotExist(err) {
		t.Fatal("a failed switch saved the stream rate")
	}
}

func TestVideoSettingsSwitchUndoesTheActivation(t *testing.T) {
	stand := newSwitchStand(t, "qhd", 1440)
	stand.programErr = errors.New("monitor EDID programming failed")
	rsp := postVideo(t, `{"monitor":1080,"fitVideoMemory":true}`)
	if rsp.Code != -4 || rsp.Data.Restart {
		t.Fatalf("response %+v", rsp)
	}
	// The previous selection is activated again.
	if !slices.Equal(stand.activated, []string{"fhd", "qhd"}) || stand.held != 0 || stand.restarts != 0 {
		t.Fatalf("stand %+v", stand)
	}
}

func TestSetVideoMemoryUsesTheActivationHelper(t *testing.T) {
	stand := newSwitchStand(t, "fhd", 1080)
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/", bytes.NewBufferString(`{"mode":"qhd"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	(&Service{}).SetVideoMemory(c)
	if !slices.Equal(stand.activated, []string{"qhd"}) {
		t.Fatalf("activated %v: %s", stand.activated, recorder.Body.String())
	}
}

func TestVideoCapabilitiesDescribeTheVideoMemory(t *testing.T) {
	root, put := videoMemoryRoot(t)
	putImages(put, "pcie", "pcie-uhd")
	put(dtBase+"nanokvm,video-memory-mode", "fhd\x00")
	caps := videoMemoryCapabilities(readVideoMemoryStatus(root))
	modes := caps["modes"].([]videoMemoryModeCapability)
	want := []videoMemoryModeCapability{{"fhd", 40, true}, {"qhd", 56, false}, {"uhd", 104, true}}
	if caps["active"] != "fhd" || caps["selected"] != "fhd" || !slices.Equal(modes, want) {
		t.Fatalf("capabilities %+v", caps)
	}
}
