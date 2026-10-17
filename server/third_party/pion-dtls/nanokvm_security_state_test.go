package dtls

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/gob"
	"errors"
	"testing"

	"github.com/pion/dtls/v3/pkg/protocol"
	"github.com/pion/dtls/v3/pkg/protocol/alert"
	"github.com/pion/dtls/v3/pkg/protocol/recordlayer"
	"github.com/pion/logging"
)

func TestNanoKVMSessionPolicyChanges(t *testing.T) {
	key := []byte("original secret")
	cfg := &handshakeConfig{
		localCipherSuites: []CipherSuite{cipherSuiteForID(TLS_PSK_WITH_AES_128_GCM_SHA256, nil)},
		localPSKCallback: func(hint []byte) ([]byte, error) {
			if !bytes.Equal(hint, []byte("peer")) {
				return nil, errors.New("unknown identity")
			}
			return key, nil
		},
		extendedMasterSecret: RequireExtendedMasterSecret,
	}
	valid := Session{ID: []byte("session"), Secret: make([]byte, 48), Version: 1,
		CipherSuiteID: TLS_PSK_WITH_AES_128_GCM_SHA256, ExtendedMasterSecret: true,
		IdentityHint: []byte("peer"), PSKHash: sha256.Sum256(key)}
	if !valid.usable(cfg, false) {
		t.Fatal("valid session rejected")
	}
	for _, change := range []func(*Session){
		func(s *Session) { s.Version = 0 }, func(s *Session) { s.IsClient = true },
		func(s *Session) { s.ExtendedMasterSecret = false }, func(s *Session) { s.Secret = s.Secret[:47] },
		func(s *Session) { s.CipherSuiteID = TLS_PSK_WITH_AES_128_CCM_8 },
		func(s *Session) { s.IdentityHint = []byte("revoked") },
	} {
		session := valid
		change(&session)
		if session.usable(cfg, false) {
			t.Fatal("accepted incompatible or unauthenticated cached session")
		}
	}
	key = []byte("rotated secret")
	if valid.usable(cfg, false) {
		t.Fatal("resumed with rotated PSK")
	}
}

func TestNanoKVMRejectsIncompleteExportedState(t *testing.T) {
	if _, err := (&State{}).MarshalBinary(); !errors.Is(err, errInvalidExportedState) {
		t.Fatal(err)
	}
	var legacy bytes.Buffer
	if err := gob.NewEncoder(&legacy).Encode(serializedState{}); err != nil {
		t.Fatal(err)
	}
	if err := (&State{}).UnmarshalBinary(legacy.Bytes()); !errors.Is(err, errInvalidExportedState) {
		t.Fatal(err)
	}
}

func TestNanoKVMDropsUnauthenticatedPostHandshakeRecords(t *testing.T) {
	c := &Conn{log: logging.NewDefaultLoggerFactory().NewLogger("dtls")}
	c.state.remoteEpoch.Store(uint16(1))
	c.handshakeCompletedSuccessfully.Store(true)
	for _, epoch := range []uint16{0, 2, 65535} {
		wire, err := (&recordlayer.RecordLayer{Header: recordlayer.Header{Version: protocol.Version1_2, Epoch: epoch},
			Content: &alert.Alert{Level: alert.Fatal, Description: alert.CloseNotify}}).Marshal()
		if err != nil {
			t.Fatal(err)
		}
		_, _, a, err := c.handleIncomingPacket(context.Background(), wire, nil, true)
		if err != nil || a != nil {
			t.Fatalf("epoch %d terminated a completed connection: %v %v", epoch, a, err)
		}
		if len(c.encryptedPackets) != 0 || len(c.state.replayDetector) != 0 {
			t.Fatal("discarded epoch allocated replay or packet state")
		}
	}
}
