package bmc

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// These are accepted by the server's actual Viper loader. The daemon must
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
			v := viper.New()
			v.SetConfigType("yaml")
			if err := v.ReadConfig(strings.NewReader(data)); err != nil {
				t.Fatal(err)
			}
			want := v.GetInt("port.http")
			if want != 8080 {
				t.Fatalf("server port = %d", want)
			}
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
