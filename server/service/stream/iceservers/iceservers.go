// Package iceservers builds the STUN and TURN servers that every NanoKVM
// WebRTC peer uses, from the stun and turn entries of the server
// configuration (/etc/kvm/server.yaml).
package iceservers

import (
	"strings"

	"github.com/pion/webrtc/v4"

	"NanoKVM-Server/config"
)

// FromConfig returns the servers in use; they may change at run time.
func FromConfig() []webrtc.ICEServer {
	settings := config.WebRTCSettings()
	return List(settings.Stun, settings.Turn)
}

// List returns the STUN servers, a list separated by commas or spaces
// ("disable" or an empty list for none), and the TURN server when its address
// and both credentials are set.
func List(stun string, turn config.Turn) []webrtc.ICEServer {
	var servers []webrtc.ICEServer
	var urls []string
	for _, server := range strings.FieldsFunc(stun, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		if server != "disable" {
			urls = append(urls, "stun:"+server)
		}
	}
	if len(urls) > 0 {
		servers = append(servers, webrtc.ICEServer{URLs: urls})
	}
	if turn.TurnAddr != "" && turn.TurnUser != "" && turn.TurnCred != "" {
		servers = append(servers, webrtc.ICEServer{
			URLs:       []string{"turn:" + turn.TurnAddr},
			Username:   turn.TurnUser,
			Credential: turn.TurnCred,
		})
	}
	return servers
}
