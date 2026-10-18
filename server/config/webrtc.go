package config

import (
	"sync"

	"gopkg.in/yaml.v3"
)

// DTLS cipher preferences of the WebRTC peers: the suite NanoKVM offers
// first; the other one stays as a fallback.
const (
	DTLSChaCha20 = "chacha20"
	DTLSAESGCM   = "aes-gcm"
)

// WebRTC is the network setup of every WebRTC peer.
type WebRTC struct {
	Stun       string
	Turn       Turn
	DTLSCipher string
}

// The WebRTC settings change at run time (Settings > WebRTC); the peers
// read them for each new connection.
var webrtcMu sync.RWMutex

func dtlsCipherOrDefault(cipher string) string {
	if cipher == DTLSAESGCM {
		return cipher
	}
	return DTLSChaCha20
}

// WebRTCSettings returns the settings in use.
func WebRTCSettings() WebRTC {
	conf := GetInstance()
	webrtcMu.RLock()
	defer webrtcMu.RUnlock()
	return WebRTC{Stun: conf.Stun, Turn: conf.Turn, DTLSCipher: dtlsCipherOrDefault(conf.DTLSCipher)}
}

// UpdateWebRTC stores the settings in server.yaml, keeping every other key,
// and applies them to new connections. Stun "" stands for the default set
// and "disable" for none.
func UpdateWebRTC(w WebRTC) error {
	conf := GetInstance()
	webrtcMu.Lock()
	defer webrtcMu.Unlock()
	w.DTLSCipher = dtlsCipherOrDefault(w.DTLSCipher)
	err := editFile(func(root *yaml.Node) {
		setScalar(root, "stun", w.Stun)
		turnNode := mappingChild(root, "turn")
		setScalar(turnNode, "turnAddr", w.Turn.TurnAddr)
		setScalar(turnNode, "turnUser", w.Turn.TurnUser)
		setScalar(turnNode, "turnCred", w.Turn.TurnCred)
		setScalar(root, "dtlsCipher", w.DTLSCipher)
	})
	if err != nil {
		return err
	}
	conf.Stun = stunOrDefault(w.Stun)
	conf.Turn = w.Turn
	conf.DTLSCipher = w.DTLSCipher
	return nil
}
