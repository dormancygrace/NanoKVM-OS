package network

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"NanoKVM-Server/config"
)

type iceState struct {
	stun   string
	turn   config.Turn
	cipher string
}

func fakeICE(t *testing.T, stun string, turn config.Turn) *iceState {
	t.Helper()
	state := &iceState{stun, turn, config.DTLSChaCha20}
	oldRead, oldUpdate := readWebRTC, updateWebRTC
	t.Cleanup(func() { readWebRTC, updateWebRTC = oldRead, oldUpdate })
	readWebRTC = func() config.WebRTC {
		return config.WebRTC{Stun: state.stun, Turn: state.turn, DTLSCipher: state.cipher}
	}
	updateWebRTC = func(w config.WebRTC) error {
		if w.Stun == "" {
			w.Stun = config.DefaultStun
		}
		state.stun, state.turn, state.cipher = w.Stun, w.Turn, w.DTLSCipher
		return nil
	}
	return state
}

func postWebRTCServers(t *testing.T, body string) (int, webrtcServersRsp, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/", bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	(&Service{}).SetWebRTCServers(c)
	var result struct {
		Code int              `json:"code"`
		Msg  string           `json:"msg"`
		Data webrtcServersRsp `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("response %q: %v", recorder.Body.String(), err)
	}
	return result.Code, result.Data, result.Msg
}

func TestWebRTCServersState(t *testing.T) {
	fakeICE(t, config.DefaultStun, config.Turn{TurnAddr: "turn.example:3478", TurnUser: "alice", TurnCred: "secret"})
	state := webrtcServersState()
	if state.StunMode != "default" || len(state.Stun) != 2 || len(state.DefaultStun) != 2 {
		t.Fatalf("default: %+v", state)
	}
	if !state.Turn.HasCredential || state.Turn.Username != "alice" {
		t.Fatalf("turn: %+v", state.Turn)
	}
	encoded, _ := json.Marshal(state)
	if bytes.Contains(encoded, []byte("secret")) {
		t.Fatal("the TURN password left the device")
	}
	fakeICE(t, "disable", config.Turn{})
	if state = webrtcServersState(); state.StunMode != "off" || len(state.Stun) != 0 {
		t.Fatalf("off: %+v", state)
	}
	fakeICE(t, "a.example:3478", config.Turn{})
	if state = webrtcServersState(); state.StunMode != "custom" || state.Stun[0] != "a.example:3478" {
		t.Fatalf("custom: %+v", state)
	}
}

func TestSetWebRTCServers(t *testing.T) {
	state := fakeICE(t, config.DefaultStun, config.Turn{})
	for _, bad := range []string{
		`{"stunMode":"default","dtlsCipher":"rc4"}`,
		`{"stunMode":"sometimes","dtlsCipher":"chacha20"}`,
		`{"dtlsCipher":"chacha20","stunMode":"custom","stun":[]}`,
		`{"dtlsCipher":"chacha20","stunMode":"custom","stun":["a.example"]}`,
		`{"dtlsCipher":"chacha20","stunMode":"custom","stun":["a.example:0"]}`,
		`{"dtlsCipher":"chacha20","stunMode":"custom","stun":["a b:3478"]}`,
		`{"dtlsCipher":"chacha20","stunMode":"custom","stun":["a:1","b:1","c:1","d:1","e:1"]}`,
		`{"dtlsCipher":"chacha20","stunMode":"default","turn":{"address":"turn.example:3478","username":"alice"}}`,
		`{"dtlsCipher":"chacha20","stunMode":"default","turn":{"address":"turn.example:3478","username":"","credential":"x"}}`,
	} {
		if code, _, _ := postWebRTCServers(t, bad); code != -1 || state.stun != config.DefaultStun || state.turn != (config.Turn{}) {
			t.Fatalf("%s: code %d, state %+v", bad, code, state)
		}
	}
	code, rsp, msg := postWebRTCServers(t, `{"dtlsCipher":"aes-gcm","stunMode":"custom","stun":["stun:A.example:3478"," b.example:19302 ","A.example:3478","[2001:db8::1]:3478"],`+
		`"turn":{"address":"turn:turn.example:3478","username":"alice","credential":"secret"}}`)
	if code != 0 || state.stun != "A.example:3478,b.example:19302,[2001:db8::1]:3478" {
		t.Fatalf("custom: %d %s, stored %q", code, msg, state.stun)
	}
	if state.turn != (config.Turn{TurnAddr: "turn.example:3478", TurnUser: "alice", TurnCred: "secret"}) || !rsp.Turn.HasCredential ||
		state.cipher != config.DTLSAESGCM || rsp.DTLSCipher != config.DTLSAESGCM {
		t.Fatalf("turn: %+v, rsp %+v", state.turn, rsp.Turn)
	}
	// An empty password keeps the stored one; an empty address removes TURN.
	if code, _, _ = postWebRTCServers(t, `{"dtlsCipher":"chacha20","stunMode":"off","turn":{"address":"turn.example:3478","username":"bob","credential":""}}`); code != 0 ||
		state.stun != "disable" || state.turn.TurnCred != "secret" || state.turn.TurnUser != "bob" {
		t.Fatalf("keep password: %d %+v", code, state)
	}
	// A page from before the cipher choice sends none: ChaCha20.
	state.cipher = config.DTLSAESGCM
	if code, _, _ = postWebRTCServers(t, `{"stunMode":"default","turn":{"address":""}}`); code != 0 || state.stun != config.DefaultStun ||
		state.turn != (config.Turn{}) || state.cipher != config.DTLSChaCha20 {
		t.Fatalf("defaults: %d %+v", code, state)
	}
}

func TestConcurrentWebRTCSaveRetainsLatestCredential(t *testing.T) {
	oldRead, oldUpdate := readWebRTC, updateWebRTC
	defer func() { readWebRTC, updateWebRTC = oldRead, oldUpdate }()
	var mu sync.Mutex
	state := config.WebRTC{Stun: config.DefaultStun, Turn: config.Turn{TurnAddr: "turn.example:3478", TurnUser: "alice", TurnCred: "old-secret"}}
	readWebRTC = func() config.WebRTC { mu.Lock(); defer mu.Unlock(); return state }
	entered, release, written := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	updateWebRTC = func(w config.WebRTC) error {
		first := calls.Add(1) == 1
		if first {
			close(entered)
			<-release
		} else {
			<-written
		}
		mu.Lock()
		state = w
		mu.Unlock()
		if first {
			close(written)
		}
		return nil
	}
	result := make(chan int, 2)
	go func() {
		code, _, _ := postWebRTCServers(t, `{"stunMode":"default","turn":{"address":"turn.example:3478","username":"alice","credential":"new-secret"}}`)
		result <- code
	}()
	<-entered
	go func() {
		code, _, _ := postWebRTCServers(t, `{"stunMode":"default","turn":{"address":"turn.example:3478","username":"bob","credential":""}}`)
		result <- code
	}()
	time.Sleep(100 * time.Millisecond)
	close(release)
	for range 2 {
		if code := <-result; code != 0 {
			t.Fatalf("save failed: %d", code)
		}
	}
	if got := readWebRTC().Turn; got.TurnCred != "new-secret" || got.TurnUser != "bob" {
		t.Fatalf("retained stale credential: %+v", got)
	}
}
