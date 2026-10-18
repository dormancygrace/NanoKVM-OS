package iceservers

import (
	"testing"

	"NanoKVM-Server/config"
)

func TestList(t *testing.T) {
	turn := config.Turn{TurnAddr: "turn.example:3478", TurnUser: "user", TurnCred: "secret"}
	tests := []struct {
		name string
		stun string
		turn config.Turn
		urls []string
	}{
		{"default STUN", "stun.l.google.com:19302", config.Turn{}, []string{"stun:stun.l.google.com:19302"}},
		{"STUN disabled", "disable", config.Turn{}, nil},
		{"no STUN", "", config.Turn{}, nil},
		{"STUN and TURN", "stun.example:3478", turn, []string{"stun:stun.example:3478", "turn:turn.example:3478"}},
		{"TURN alone", "disable", turn, []string{"turn:turn.example:3478"}},
		{"TURN without a credential", "disable", config.Turn{TurnAddr: "turn.example:3478", TurnUser: "user"}, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			servers := List(test.stun, test.turn)
			if len(servers) != len(test.urls) {
				t.Fatalf("servers %+v, want %v", servers, test.urls)
			}
			for i, server := range servers {
				if len(server.URLs) != 1 || server.URLs[0] != test.urls[i] {
					t.Fatalf("server %d: %+v, want %s", i, server, test.urls[i])
				}
				turnServer := test.urls[i][:5] == "turn:"
				if turnServer != (server.Username == "user" && server.Credential == "secret") {
					t.Fatalf("server %d credentials: %+v", i, server)
				}
			}
		})
	}
}
