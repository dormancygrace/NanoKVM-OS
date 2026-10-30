package mcpservice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"NanoKVM-Server/service/controlmode"
	"NanoKVM-Server/service/inputcontrol"
)

type fakeHostControl struct {
	mu        sync.Mutex
	log       []string
	mounted   string
	noStorage bool
}

func (f *fakeHostControl) record(entry string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, entry)
}

func (f *fakeHostControl) Do(_ context.Context, action string) error {
	f.record("power " + action)
	return nil
}

func (f *fakeHostControl) Images() ([]string, error) { return []string{"/data/debian.iso"}, nil }

func (f *fakeHostControl) MountedImage() (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.noStorage {
		return "", false, errors.New("USB storage is off")
	}
	return f.mounted, f.mounted != "", nil
}

func (f *fakeHostControl) MountImageFile(name string, cdrom bool) error {
	f.record(fmt.Sprintf("mount %q cdrom=%v", name, cdrom))
	f.mu.Lock()
	f.mounted = name
	f.mu.Unlock()
	return nil
}

func (f *fakeHostControl) wake(mac, iface string) error {
	f.record("wake " + mac + " " + iface)
	return nil
}

func (f *fakeHostControl) entries() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.log, "; ")
}

func TestHostControlToolsFollowTheSwitch(t *testing.T) {
	useTestConfig(t)
	fake := &fakeHostControl{}
	control := controlmode.NewManager(filepath.Join(t.TempDir(), "mode"), controlmode.ModeMCP)
	handler := newMCPHandler(control, &inputcontrol.Coordinator{}, newRemoteWithHID(&recordingHID{}), nil, nil, nil,
		HostControl{Power: fake, Media: fake, Wake: fake.wake})
	call := func(id int, method, params string) string {
		recorder := httptest.NewRecorder()
		body := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":%s}`, id, method, params)
		req := httptest.NewRequest(http.MethodPost, "/api/mcp", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		handler.ServeHTTP(recorder, req)
		return recorder.Body.String()
	}
	tool := func(id int, name, arguments string) string {
		return call(id, "tools/call", fmt.Sprintf(`{"name":%q,"arguments":%s}`, name, arguments))
	}
	controlTools := []string{"power", "virtual_media", "wake_on_lan"}

	// Off (the default): neither listed nor callable.
	list := call(1, "tools/list", `{}`)
	for _, name := range controlTools {
		if strings.Contains(list, `"name":"`+name+`"`) {
			t.Fatalf("switch off, tools/list offers %s: %s", name, list)
		}
	}
	if reply := tool(2, "power", `{"action":"on"}`); !strings.Contains(reply, "error") {
		t.Fatalf("switch off, power call answered: %s", reply)
	}
	if fake.entries() != "" {
		t.Fatalf("switch off, the host was touched: %s", fake.entries())
	}

	if _, err := updateConfig(func(cfg Config) (Config, error) { cfg.AllowHostControl = true; return cfg, nil }); err != nil {
		t.Fatal(err)
	}
	list = call(3, "tools/list", `{}`)
	for _, name := range append(controlTools, "screenshot", "host_status") {
		if !strings.Contains(list, `"name":"`+name+`"`) {
			t.Fatalf("switch on, tools/list lacks %s: %s", name, list)
		}
	}
	for i, c := range []struct{ name, arguments, want string }{
		{"power", `{"action":"on"}`, `"ok":true`},
		{"power", `{"action":"force_off"}`, `"ok":true`},
		{"power", `{"action":"cycle"}`, `"ok":true`},
		{"virtual_media", `{"action":"list"}`, `/data/debian.iso`},
		{"virtual_media", `{"action":"mount","file":"/data/debian.iso","cdrom":true}`, `"mounted":"/data/debian.iso"`},
		{"virtual_media", `{"action":"eject"}`, `"mounted":""`},
		{"wake_on_lan", `{"mac":"02:00:00:00:00:01"}`, `"ok":true`},
	} {
		if reply := tool(10+i, c.name, c.arguments); !strings.Contains(reply, c.want) || strings.Contains(reply, `"isError":true`) {
			t.Fatalf("%s %s: %s", c.name, c.arguments, reply)
		}
	}
	want := `power On; power ForceOff; power PowerCycle; mount "/data/debian.iso" cdrom=true; mount "" cdrom=false; wake 02:00:00:00:00:01 `
	if got := fake.entries(); got != want {
		t.Fatalf("host actions:\n got %s\nwant %s", got, want)
	}
	for i, c := range []struct{ name, arguments string }{
		{"power", `{"action":"explode"}`},
		{"virtual_media", `{"action":"mount"}`},
		{"wake_on_lan", `{}`},
	} {
		if reply := tool(30+i, c.name, c.arguments); !strings.Contains(reply, `"isError":true`) {
			t.Fatalf("invalid %s %s answered: %s", c.name, c.arguments, reply)
		}
	}

	// Switched off again: gone with the next request.
	if _, err := updateConfig(func(cfg Config) (Config, error) { cfg.AllowHostControl = false; return cfg, nil }); err != nil {
		t.Fatal(err)
	}
	if list = call(40, "tools/list", `{}`); strings.Contains(list, `"name":"power"`) {
		t.Fatalf("switched off again, tools/list offers power: %s", list)
	}
}

// Without USB storage the list still comes, with the reason a mount would fail
// (found on the stand: the tool answered only "read failed").
func TestVirtualMediaListWithoutUSBStorage(t *testing.T) {
	useTestConfig(t)
	if _, err := setHostControl(true); err != nil {
		t.Fatal(err)
	}
	fake := &fakeHostControl{noStorage: true}
	_, result, err := virtualMediaHandler(nil, fake)(context.Background(), nil, VirtualMediaParams{Action: "list"})
	if err != nil || !result.OK || len(result.Images) != 1 || result.Unavailable == "" {
		t.Fatalf("list without USB storage: %+v, %v", result, err)
	}
}

// gatedBody holds the request body back until released, like a slow client.
type gatedBody struct {
	reading chan struct{}
	resume  chan struct{}
	once    sync.Once
	body    *bytes.Reader
}

func (b *gatedBody) Read(p []byte) (int, error) {
	b.once.Do(func() {
		close(b.reading)
		<-b.resume
	})
	return b.body.Read(p)
}

// The request picks its tools while the switch is on, the switch goes off
// before its body arrives: the call is refused and the host untouched
// (review of #572).
func TestHostControlSwitchedOffDuringTheRequest(t *testing.T) {
	useTestConfig(t)
	if _, err := setHostControl(true); err != nil {
		t.Fatal(err)
	}
	fake := &fakeHostControl{}
	control := controlmode.NewManager(filepath.Join(t.TempDir(), "mode"), controlmode.ModeMCP)
	handler := newMCPHandler(control, &inputcontrol.Coordinator{}, newRemoteWithHID(&recordingHID{}), nil, nil, nil,
		HostControl{Power: fake, Media: fake, Wake: fake.wake})
	for i, call := range []string{
		`{"name":"power","arguments":{"action":"force_off"}}`,
		`{"name":"virtual_media","arguments":{"action":"mount","file":"/data/debian.iso"}}`,
		`{"name":"virtual_media","arguments":{"action":"list"}}`,
		`{"name":"wake_on_lan","arguments":{"mac":"02:00:00:00:00:01"}}`,
	} {
		if _, err := setHostControl(true); err != nil {
			t.Fatal(err)
		}
		body := &gatedBody{reading: make(chan struct{}), resume: make(chan struct{}),
			body: bytes.NewReader([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":%s}`, i+1, call)))}
		req := httptest.NewRequest(http.MethodPost, "/api/mcp", body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		recorder := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			handler.ServeHTTP(recorder, req)
			close(done)
		}()
		<-body.reading
		if _, err := setHostControl(false); err != nil {
			t.Fatal(err)
		}
		close(body.resume)
		<-done
		if reply := recorder.Body.String(); !strings.Contains(reply, `"isError":true`) || !strings.Contains(reply, "host control is off") {
			t.Fatalf("%s after switching off: %s", call, reply)
		}
	}
	if fake.entries() != "" {
		t.Fatalf("switched off, the host was touched: %s", fake.entries())
	}
}

// blockingPower holds a power action until released.
type blockingPower struct {
	started chan struct{}
	finish  chan struct{}
}

func (p *blockingPower) Do(context.Context, string) error {
	close(p.started)
	<-p.finish
	return nil
}

// Switching off waits for the operation running, so once it has answered
// nothing acts on the host any more.
func TestHostControlSwitchOffWaitsForTheOperation(t *testing.T) {
	useTestConfig(t)
	if _, err := setHostControl(true); err != nil {
		t.Fatal(err)
	}
	power := &blockingPower{started: make(chan struct{}), finish: make(chan struct{})}
	executor := &toolExecutor{control: controlmode.NewManager(filepath.Join(t.TempDir(), "mode"), controlmode.ModeMCP)}
	pressed := make(chan error, 1)
	go func() {
		_, _, err := powerHandler(executor, power)(context.Background(), nil, PowerParams{Action: "cycle"})
		pressed <- err
	}()
	<-power.started
	switched := make(chan error, 1)
	go func() {
		_, err := setHostControl(false)
		switched <- err
	}()
	select {
	case <-switched:
		t.Fatal("switched off while the power action was running")
	case <-time.After(50 * time.Millisecond):
	}
	close(power.finish)
	if err := <-pressed; err != nil {
		t.Fatal(err)
	}
	if err := <-switched; err != nil {
		t.Fatal(err)
	}
	if _, _, err := powerHandler(executor, power)(context.Background(), nil, PowerParams{Action: "on"}); err == nil {
		t.Fatal("power ran after switching off")
	}
}

// Outside MCP mode a control tool presses nothing.
func TestHostControlNeedsMCPMode(t *testing.T) {
	fake := &fakeHostControl{}
	control := controlmode.NewManager(filepath.Join(t.TempDir(), "mode"), controlmode.ModeOff)
	executor := &toolExecutor{control: control}
	if _, _, err := powerHandler(executor, fake)(context.Background(), nil, PowerParams{Action: "on"}); err == nil {
		t.Fatal("power ran outside MCP mode")
	}
	if _, _, err := wakeOnLANHandler(executor, fake.wake)(context.Background(), nil, WakeOnLANParams{MAC: "02:00:00:00:00:01"}); err == nil {
		t.Fatal("wake ran outside MCP mode")
	}
	if fake.entries() != "" {
		t.Fatalf("host touched outside MCP mode: %s", fake.entries())
	}
}
