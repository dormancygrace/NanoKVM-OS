// Package iceservers builds the STUN and TURN servers that every NanoKVM
// WebRTC peer uses, from the stun and turn entries of the server
// configuration (/etc/kvm/server.yaml).
package iceservers

import (
	"github.com/pion/webrtc/v4"

	"NanoKVM-Server/config"
)

// FromConfig returns the servers of the loaded configuration.
func FromConfig() []webrtc.ICEServer {
	conf := config.GetInstance()
	return List(conf.Stun, conf.Turn)
}

// List returns the STUN server unless it is empty or "disable", and the TURN
// server when its address and both credentials are set.
func List(stun string, turn config.Turn) []webrtc.ICEServer {
	var servers []webrtc.ICEServer
	if stun != "" && stun != "disable" {
		servers = append(servers, webrtc.ICEServer{URLs: []string{"stun:" + stun}})
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
