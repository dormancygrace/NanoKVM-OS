package acme

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	log "github.com/sirupsen/logrus"
)

// ChallengePrefix is the HTTP-01 path a CA fetches tokens from.
const ChallengePrefix = "/.well-known/acme-challenge/"

var tokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// EnsureChallengeDir creates the webroot acme.sh writes tokens into (on
// tmpfs; tokens are short-lived).
func EnsureChallengeDir() {
	if err := os.MkdirAll(filepath.Join(challengeRoot, ".well-known", "acme-challenge"), 0o755); err != nil {
		log.Errorf("failed to create the ACME challenge directory: %v", err)
	}
}

// ChallengeHandler serves the token files, and nothing else, without
// authentication: the CA fetches them over plain HTTP on port 80 and over
// HTTPS, so both listeners mount it.
func ChallengeHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		token := strings.TrimPrefix(r.URL.Path, ChallengePrefix)
		if !strings.HasPrefix(r.URL.Path, ChallengePrefix) || !tokenRe.MatchString(token) {
			http.NotFound(w, r)
			return
		}
		path := filepath.Join(challengeRoot, ".well-known", "acme-challenge", token)
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}
		data, err := os.ReadFile(path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(data)
	})
}
