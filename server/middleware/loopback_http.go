package middleware

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"

	"NanoKVM-Server/config"
	"NanoKVM-Server/utils"
)

// ListenAndServeLoopbackHTTPRedirect serves the plain-HTTP port while HTTPS
// is on: loopback callers with the internal token reach the allowed paths,
// challenge (when set) answers requests under its prefix without
// authentication (ACME HTTP-01 tokens, fetched by the CA over port 80), and
// everything else is redirected to HTTPS.
func ListenAndServeLoopbackHTTPRedirect(
	httpAddr string,
	httpsPort string,
	handler http.Handler,
	challenge http.Handler,
	challengePrefix string,
	allowedPaths ...string,
) error {
	allowlist := make(map[string]struct{}, len(allowedPaths))
	for _, path := range allowedPaths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		allowlist[path] = struct{}{}
	}

	return utils.NewHTTPServer(httpAddr, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if isLoopbackAllowedPath(req, allowlist) {
			if hasValidLoopbackHTTPToken(req) {
				handler.ServeHTTP(w, req)
				return
			}

			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if challenge != nil && challengePrefix != "" && strings.HasPrefix(req.URL.Path, challengePrefix) {
			challenge.ServeHTTP(w, req)
			return
		}

		http.Redirect(w, req, "https://"+redirectHost(req.Host, httpsPort)+req.URL.RequestURI(), http.StatusTemporaryRedirect)
	}), true).ListenAndServe()
}

func redirectHost(requestHost string, httpsPort string) string {
	host := requestHost
	if h, _, err := net.SplitHostPort(requestHost); err == nil {
		host = h
	} else if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	}

	if httpsPort != "443" {
		return net.JoinHostPort(host, httpsPort)
	}

	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") && !strings.HasSuffix(host, "]") {
		return "[" + host + "]"
	}

	return host
}

func allowByLoopbackInternalToken(req *http.Request) bool {
	return req != nil && isLoopbackRemote(req.RemoteAddr) && hasValidLoopbackHTTPToken(req)
}

func isLoopbackAllowedPath(req *http.Request, allowedPaths map[string]struct{}) bool {
	if !allowByLoopbackInternalToken(req) {
		return false
	}

	_, allowed := allowedPaths[req.URL.Path]
	return allowed
}

func hasValidLoopbackHTTPToken(req *http.Request) bool {
	if req == nil {
		return false
	}
	token, err := config.GetPicoclawInternalToken()
	if err != nil || token == "" {
		return false
	}

	provided := req.Header.Get(config.PicoclawInternalTokenHeader)
	return subtle.ConstantTimeCompare([]byte(provided), []byte(token)) == 1
}

func isLoopbackRemote(remoteAddr string) bool {
	host := strings.TrimSpace(remoteAddr)
	if parsedHost, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = parsedHost
	}

	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
