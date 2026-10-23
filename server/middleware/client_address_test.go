package middleware

import (
	"net/http/httptest"
	"testing"

	"NanoKVM-Server/config"
)

func TestClientAddress(t *testing.T) {
	conf := config.GetInstance()
	original := conf.Security.TrustedProxies
	conf.Security.TrustedProxies = []string{"127.0.0.1/32", "::1/128", "10.0.0.5"}
	defer func() { conf.Security.TrustedProxies = original }()

	for _, test := range []struct {
		name, remote string
		forwarded    []string
		want         string
	}{
		{"direct peer, forwarded ignored", "192.0.2.7:4000", []string{"198.51.100.1"}, "192.0.2.7"},
		{"proxy, its client", "127.0.0.1:4000", []string{"100.64.0.9"}, "100.64.0.9"},
		{"client-supplied entries to the left are skipped", "127.0.0.1:4000", []string{"6.6.6.6, 100.64.0.9"}, "100.64.0.9"},
		{"chained trusted proxies", "127.0.0.1:4000", []string{"100.64.0.9", "10.0.0.5"}, "100.64.0.9"},
		{"IPv6 client", "[::1]:4000", []string{"2001:db8::7"}, "2001:db8::7"},
		{"mapped IPv4", "127.0.0.1:4000", []string{"::ffff:100.64.0.9"}, "100.64.0.9"},
		{"malformed entry: the proxy", "127.0.0.1:4000", []string{"100.64.0.9, nonsense"}, "127.0.0.1"},
		{"no header: the proxy", "127.0.0.1:4000", nil, "127.0.0.1"},
	} {
		r := httptest.NewRequest("POST", "/api/auth/login", nil)
		r.RemoteAddr = test.remote
		for _, value := range test.forwarded {
			r.Header.Add("X-Forwarded-For", value)
		}
		if got := ClientAddress(r); got != test.want {
			t.Errorf("%s: %q, want %q", test.name, got, test.want)
		}
	}
}
