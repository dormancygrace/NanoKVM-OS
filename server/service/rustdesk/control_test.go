package rustdesk

import (
	"net"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"NanoKVM-Server/service/controlmode"
	"NanoKVM-Server/service/inputcontrol"
	"NanoKVM-Server/service/ws"
)

// Sends bytes on a new control connection and returns the frame's status.
func controlExchange(t *testing.T, b *Bridge, data []byte) byte {
	t.Helper()
	client, server := net.Pipe()
	defer client.Close()
	go func() { defer server.Close(); b.serveControl(server) }()
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	var status [1]byte
	if _, err := client.Write(data); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Read(status[:]); err != nil {
		t.Fatal(err)
	}
	return status[0]
}

// The bytes of control_channel_bytes_match_the_bridge in
// addons/rustdesk/src/nanokvm.rs: the add-on encodes what is decoded here.
func TestControlChannel(t *testing.T) {
	const id = "00112233445566778899aabbccddeeff"
	hello := []byte{0x4f, 0x4b, 0x48, 0x43, 0x01, 0x00, 0x00, 0x00,
		0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	frame := []byte{0x03, 0x01, 0x02, 0x04, 0x05, 0x00, 0x00, 0x00, 0x00, 0x02, 0x00, 0xfd, 0x7f, 0xff,
		0x00, 0x00, 0x00, 0x03, 0x01, 0x34, 0x12, 0xff, 0x7f, 0x01, 0x00}
	b := NewBridge()
	var written [][]byte
	s := &hidSession{id: id, touched: time.Now(), manual: inputcontrol.NewManualSession(controlmode.NewManager(filepath.Join(t.TempDir(), "mode"), controlmode.ModeOff), nil),
		writeHID: func(_ inputcontrol.ManualReportKind, report []byte) error {
			written = append(written, append([]byte(nil), report...))
			return nil
		}}
	if !ws.GetManager().AcquireExternalInput(id, s.close) {
		t.Fatal("input lease is taken")
	}
	t.Cleanup(s.close)
	b.sessions[id] = s

	status := controlExchange(t, b, append(append([]byte(nil), hello...), frame...))
	reports := [][]byte{{0x02, 0, 4, 5, 0, 0, 0, 0}, {0, 0xfd, 0x7f, 0xff, 0}, {1, 0x34, 0x12, 0xff, 0x7f, 1, 0}}
	if status != 0 || !reflect.DeepEqual(written, reports) {
		t.Fatalf("status %d, reports %x", status, written)
	}

	// A second session finds the lease held, and nothing of it is written.
	other := append([]byte(nil), hello...)
	other[23] = 0
	if status := controlExchange(t, b, append(other, 1, 1, 0, 4, 0, 0, 0, 0, 0)); status != 1 || len(written) != 3 {
		t.Fatalf("second session: status %d, reports %x", status, written)
	}

	// Close releases the held key and button and gives the lease up.
	status = controlExchange(t, b, append(append([]byte(nil), hello...), 1, 5, 0, 0, 0, 0, 0, 0, 0))
	released := [][]byte{{0, 0, 0, 0, 0, 0, 0, 0}, {0, 0x34, 0x12, 0xff, 0x7f, 0, 0}}
	if status != 0 || !reflect.DeepEqual(written[3:], released) || !ws.GetManager().AllowsInputLease("") {
		t.Fatalf("close: status %d, reports %x", status, written[3:])
	}
}
