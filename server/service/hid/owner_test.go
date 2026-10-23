package hid

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"

	"NanoKVM-Server/service/controlmode"
	"NanoKVM-Server/service/inputcontrol"
)

// keyboardRecorder is a Service whose keyboard reports go to a pipe.
type keyboardRecorder struct {
	service *Service
	mu      sync.Mutex
	reports [][]byte
	done    chan struct{}
	file    *os.File
}

func newKeyboardRecorder(t *testing.T) *keyboardRecorder {
	t.Helper()
	reader, file, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	r := &keyboardRecorder{
		service: &Service{
			hid:         &Hid{g0: file},
			control:     controlmode.NewManager(filepath.Join(t.TempDir(), "mode"), controlmode.ModeMCP),
			coordinator: inputcontrol.GetCoordinator(),
		},
		done: make(chan struct{}),
		file: file,
	}
	go func() {
		defer reader.Close()
		defer close(r.done)
		report := make([]byte, 8)
		for {
			if _, err := io.ReadFull(reader, report); err != nil {
				return
			}
			r.mu.Lock()
			r.reports = append(r.reports, bytes.Clone(report))
			r.mu.Unlock()
		}
	}()
	return r
}

// sent closes the pipe and returns every report written to it.
func (r *keyboardRecorder) sent() [][]byte {
	r.file.Close()
	<-r.done
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reports
}

// call runs handler with an owner check that turns false after presses
// presses have gone out, as a takeover by another tab would.
func call(t *testing.T, r *keyboardRecorder, handler gin.HandlerFunc, form url.Values, presses int) (int, [][]byte) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var checks atomic.Int32
	SetInputOwner(c, func() bool { return int(checks.Add(1)) <= presses })
	handler(c)
	var rsp struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rsp); err != nil {
		t.Fatalf("response %q: %v", w.Body.String(), err)
	}
	return rsp.Code, r.sent()
}

func pressedIn(report []byte) bool { return report[0] != 0 || report[2] != 0 }

func TestPasteStopsWhenAnotherSessionTakesControl(t *testing.T) {
	r := newKeyboardRecorder(t)
	code, reports := call(t, r, r.service.Paste, url.Values{"content": {"abcdefgh"}}, 3)
	if code != InputOwnerCode {
		t.Fatalf("code = %d, want %d", code, InputOwnerCode)
	}
	presses := 0
	for _, report := range reports {
		if pressedIn(report) {
			presses++
		}
	}
	if presses != 3 {
		t.Fatalf("%d keys went down, want the 3 before the takeover: %v", presses, reports)
	}
	if len(reports) == 0 || pressedIn(reports[len(reports)-1]) {
		t.Fatalf("the last key it pressed was not let go: %v", reports)
	}
	// Each press has its release and nothing more: no extra all-up report
	// that would release keys the new owner holds.
	if len(reports) != 6 {
		t.Fatalf("%d reports, want 3 presses and 3 releases: %v", len(reports), reports)
	}
}

func TestPasteWithControlTypesEverything(t *testing.T) {
	r := newKeyboardRecorder(t)
	code, reports := call(t, r, r.service.Paste, url.Values{"content": {"abc"}}, 1000)
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if len(reports) != 6 {
		t.Fatalf("%d reports, want 3 presses and 3 releases: %v", len(reports), reports)
	}
}

func TestSequenceStopsWhenAnotherSessionTakesControl(t *testing.T) {
	r := newKeyboardRecorder(t)
	// SysRq builds up over four presses; control moves after the second.
	code, reports := call(t, r, r.service.SendSequence, url.Values{"sequence": {"sysrq"}, "key": {"s"}}, 2)
	if code != InputOwnerCode {
		t.Fatalf("code = %d, want %d", code, InputOwnerCode)
	}
	if len(reports) != 3 || pressedIn(reports[2]) {
		t.Fatalf("want Alt, Alt+SysRq, then the release: %v", reports)
	}
}

func TestSequenceLostControlBeforeFirstPressKeepsNewOwnersKey(t *testing.T) {
	for _, sequence := range []string{"sysrq", "ctrl-alt-del"} {
		t.Run(sequence, func(t *testing.T) {
			r := newKeyboardRecorder(t)
			// The new owner pressed X after router admission, before our
			// first sequence step. This request must not release that key.
			if err := r.service.hid.WriteKeyboardReport(keyboardReport(0, 0x1b)); err != nil {
				t.Fatal(err)
			}
			code, reports := call(t, r, r.service.SendSequence,
				url.Values{"sequence": {sequence}, "key": {"s"}}, 0)
			if code != InputOwnerCode {
				t.Fatalf("code = %d, want %d", code, InputOwnerCode)
			}
			if len(reports) != 1 || reports[0][2] != 0x1b {
				t.Fatalf("request pressed nothing but changed the new owner's key: %v", reports)
			}
		})
	}
}

func TestSequenceWithControlReleasesOnlyOnce(t *testing.T) {
	r := newKeyboardRecorder(t)
	code, reports := call(t, r, r.service.SendSequence,
		url.Values{"sequence": {"ctrl-alt-del"}}, 1000)
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if len(reports) != 2 || !pressedIn(reports[0]) || pressedIn(reports[1]) {
		t.Fatalf("want one press and its release: %v", reports)
	}
}
