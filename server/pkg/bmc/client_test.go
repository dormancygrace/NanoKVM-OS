package bmc

import (
	"testing"

	"NanoKVM-Server/config"
)

// The daemon must use the port the server listens on, even when the owner
// formats server.yaml differently: each form is read by the server's own
// loader and by the daemon, and both must agree.
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
			ports, err := config.ParsePorts([]byte(data))
			if err != nil || ports.Http != 8080 {
				t.Fatalf("server port = %d (%v)", ports.Http, err)
			}
			if got := httpPort([]byte(data)); got != ports.Http {
				t.Fatalf("daemon port = %d, server port = %d", got, ports.Http)
			}
		})
	}
	for _, data := range []string{"", "port: [", "port: {http: -1}", "port: {http: 65536}", "port: {http: invalid}"} {
		if got := httpPort([]byte(data)); got != 0 {
			t.Fatalf("invalid port %q: %d", data, got)
		}
	}
}
