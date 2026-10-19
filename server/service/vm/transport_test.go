package vm

import (
	"NanoKVM-Server/authn"
	"NanoKVM-Server/common"
	"os"
	"path/filepath"
	"testing"
)

func temporaryTransport(t *testing.T) {
	t.Helper()
	oldFile, oldListeners := transportFile, transportListeners
	transportFile = filepath.Join(t.TempDir(), "transport")
	transportListeners = nil
	t.Cleanup(func() { transportFile, transportListeners = oldFile, oldListeners })
}

func TestStreamTransportStorageAndListeners(t *testing.T) {
	temporaryTransport(t)
	if StreamTransport() != TransportDirect || StreamTransportSet() {
		t.Fatal("an unset device transport is Direct")
	}
	var changes []string
	OnStreamTransportChange(func(value string) { changes = append(changes, value) })
	if err := SetStreamTransport("hls"); err == nil {
		t.Fatal("unknown transport accepted")
	}
	// Storing the default records it without a change.
	if err := SetStreamTransport(TransportDirect); err != nil || !StreamTransportSet() || len(changes) != 0 {
		t.Fatalf("store Direct: %v, set %v, changes %v", err, StreamTransportSet(), changes)
	}
	for _, value := range []string{TransportWebRTC, TransportWebRTC, TransportMJPEG} {
		if err := SetStreamTransport(value); err != nil {
			t.Fatal(err)
		}
	}
	if StreamTransport() != TransportMJPEG || len(changes) != 2 || changes[0] != TransportWebRTC || changes[1] != TransportMJPEG {
		t.Fatalf("transport %s, changes %v", StreamTransport(), changes)
	}
	if err := os.WriteFile(transportFile, []byte("garbage\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if StreamTransport() != TransportDirect {
		t.Fatal("an unreadable value is Direct")
	}
}

func TestVideoSettingsStoreTheTransport(t *testing.T) {
	temporaryTransport(t)
	newVideoStand(t, common.SharedVideoPool)
	if rsp := postVideo(t, `{"transport":"hls"}`); rsp.Code != -1 || StreamTransportSet() {
		t.Fatalf("unknown transport: %+v", rsp)
	}
	if rsp := postVideo(t, `{"transport":"webrtc"}`); rsp.Code != 0 || StreamTransport() != TransportWebRTC {
		t.Fatalf("webrtc: %+v, stored %s", rsp, StreamTransport())
	}
	// A failing setting in the same request leaves the transport unchanged.
	if rsp := postVideo(t, `{"transport":"mjpeg","fps":5}`); rsp.Code == 0 || StreamTransport() != TransportWebRTC {
		t.Fatalf("invalid batch: %+v, stored %s", rsp, StreamTransport())
	}
	if code, _ := setVideoAs(t, `{"transport":"mjpeg"}`, authn.RoleUser); code != -403 || StreamTransport() != TransportWebRTC {
		t.Fatalf("a user changed the device transport: %d", code)
	}
}
