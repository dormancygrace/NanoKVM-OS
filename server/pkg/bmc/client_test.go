package bmc

import (
	"testing"
)

// These YAML forms are accepted by the server's loader. The daemon must
// use the same port even when the owner formats server.yaml differently.
func TestHTTPPortMatchesServerYAML(t *testing.T) {
	for name, data := range map[string]string{
		"block":   "port:\n  http: 8080\n  https: 443\n",
		"comment": "port: # local listeners\n  http: 8080\n  https: 443\n",
		"flow":    "port: {http: 8080, https: 443}\n",
		"quoted":  "port: {http: '8080', https: 443}\n",
		"alias":   "listeners: &ports {http: 8080, https: 443}\nport: *ports\n",
		"merge":   "defaults: &ports {http: 8080, https: 443}\nport: {<<: *ports}\n",
	} {
		t.Run(name, func(t *testing.T) {
			want := 8080
			if got := httpPort([]byte(data)); got != want {
				t.Fatalf("daemon port = %d, server port = %d", got, want)
			}
		})
	}
	for _, data := range []string{"", "port: [", "port: {http: -1}", "port: {http: 65536}", "port: {http: invalid}"} {
		if got := httpPort([]byte(data)); got != 0 {
			t.Fatalf("invalid port %q: %d", data, got)
		}
	}
}
