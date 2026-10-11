// Package acme obtains and renews the HTTPS certificate with acme.sh from a
// public CA (Let's Encrypt, ZeroSSL, Buypass, Google). acme.sh is installed
// from its latest stable GitHub release into /etc/kvm/acme/bin and can be
// updated from the page when a newer release is out; its account, domain
// configuration and logs live in /etc/kvm/acme/data. Renewal runs from this
// server, which also owns the TLS pair, so a renewed certificate is served
// without a restart.
package acme

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"NanoKVM-Server/config"
)

const (
	// The latest stable release (GitHub leaves drafts and pre-releases out
	// of "latest") and the source archive of a tag.
	latestReleaseURL = "https://api.github.com/repos/acmesh-official/acme.sh/releases/latest"
	tarballURLPrefix = "https://github.com/acmesh-official/acme.sh/archive/refs/tags/"

	maxTarball = 4 << 20
	maxFile    = 1 << 20
)

// The layout under root; variables so tests can use a temporary tree.
var (
	root          = "/etc/kvm/acme"
	challengeRoot = "/run/nanokvm/acme-challenge"
	certFile      = config.AcmeCertFile
	keyFile       = config.AcmeKeyFile

	// One acme.sh operation at a time: install, a job, remove, uninstall.
	opMu sync.Mutex
)

func binDir() string   { return filepath.Join(root, "bin") }
func dataDir() string  { return filepath.Join(root, "data") }
func script() string   { return filepath.Join(binDir(), "acme.sh") }
func hooksDir() string { return filepath.Join(binDir(), "dnsapi") }
func certDir() string  { return filepath.Dir(certFile) }

// Installed reports whether the acme.sh script is in place.
func Installed() bool {
	info, err := os.Stat(script())
	return err == nil && info.Mode().IsRegular()
}

var versionLine = regexp.MustCompile(`(?m)^VER=(\S+)`)

// installedVersion reads VER= from the head of the installed script.
func installedVersion() string {
	data, err := os.ReadFile(script())
	if err != nil {
		return ""
	}
	return scriptVersion(data)
}

func scriptVersion(script []byte) string {
	if len(script) > 4096 {
		script = script[:4096]
	}
	m := versionLine.FindSubmatch(script)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(string(m[1]))
}
