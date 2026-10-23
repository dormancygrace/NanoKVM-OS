package acme

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"NanoKVM-Server/internal/atomicfile"
)

// Settings is what the page asked for, without credentials: acme.sh keeps the
// DNS provider credentials in its own account.conf (0600) for renewals, and
// the EAB key is only needed to register the account once.
type Settings struct {
	Domains     []string `json:"domains"`
	Email       string   `json:"email,omitempty"`
	CA          string   `json:"ca"`
	Challenge   string   `json:"challenge"`
	DNSProvider string   `json:"dnsProvider,omitempty"`
	DNSSleep    int      `json:"dnsSleep,omitempty"`
	IssuedAt    string   `json:"issuedAt,omitempty"`
}

// The CA names the page offers and the acme.sh --server names behind them.
var caServers = map[string]string{
	"letsencrypt":      "letsencrypt",
	"letsencrypt_test": "letsencrypt_test",
	"zerossl":          "zerossl",
	"buypass":          "buypass",
	"google":           "google",
	"google_test":      "googletest",
}

const (
	challengeHTTP = "http"
	challengeDNS  = "dns"
	maxDomains    = 10
	maxEnvValue   = 1024
)

var (
	// A hostname of RFC 1123 labels, optionally with a leading wildcard.
	domainRe = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`)
	emailRe  = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
)

// normalize lowercases and trims the domains and checks the settings.
func (s *Settings) normalize() error {
	if len(s.Domains) == 0 || len(s.Domains) > maxDomains {
		return fmt.Errorf("give 1 to %d domains", maxDomains)
	}
	seen := map[string]bool{}
	for i, d := range s.Domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if len(d) > 253 || !domainRe.MatchString(d) || net.ParseIP(d) != nil {
			return fmt.Errorf("%q is not a valid domain name", d)
		}
		if seen[d] {
			return fmt.Errorf("%q is listed twice", d)
		}
		seen[d] = true
		s.Domains[i] = d
	}
	s.Email = strings.TrimSpace(s.Email)
	if s.Email != "" && (len(s.Email) > 254 || !emailRe.MatchString(s.Email)) {
		return errors.New("the e-mail address is not valid")
	}
	if _, ok := caServers[s.CA]; !ok {
		return fmt.Errorf("unknown certificate authority %q", s.CA)
	}
	if s.CA == "zerossl" && s.Email == "" {
		return errors.New("ZeroSSL needs an e-mail address to register the account")
	}
	switch s.Challenge {
	case challengeHTTP:
		for _, d := range s.Domains {
			if strings.HasPrefix(d, "*.") {
				return errors.New("a wildcard certificate needs the DNS challenge")
			}
		}
		s.DNSProvider, s.DNSSleep = "", 0
	case challengeDNS:
		if !hookName.MatchString(s.DNSProvider) {
			return errors.New("choose a DNS provider")
		}
		if s.DNSSleep < 0 || s.DNSSleep > maxDNSSleep {
			return errors.New("the DNS wait must be 0 to 3600 seconds")
		}
	default:
		return errors.New("the challenge must be http or dns")
	}
	return nil
}

// primary is the domain acme.sh files the certificate under.
func (s Settings) primary() string {
	if len(s.Domains) == 0 {
		return ""
	}
	return s.Domains[0]
}

func settingsFile() string { return filepath.Join(dataDir(), "nanokvm.json") }

func loadSettings() (*Settings, error) {
	data, err := os.ReadFile(settingsFile())
	if err != nil {
		return nil, err
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func saveSettings(s *Settings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir(), 0o700); err != nil {
		return err
	}
	return atomicfile.Write(settingsFile(), data, 0o600)
}

// checkEnv keeps only the variables the hook declares, with plain values.
func checkEnv(p Provider, env map[string]string) ([]string, []string, error) {
	allowed := p.envNames()
	vars := make([]string, 0, len(env))
	secrets := make([]string, 0, len(env))
	for name, value := range env {
		if !allowed[name] {
			return nil, nil, fmt.Errorf("%s is not a setting of %s", name, p.Hook)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if len(value) > maxEnvValue || strings.ContainsAny(value, "\x00\r\n") {
			return nil, nil, fmt.Errorf("the value of %s is not usable", name)
		}
		vars = append(vars, name+"="+value)
		secrets = append(secrets, value)
	}
	if len(vars) == 0 && len(p.Options) > 0 {
		return nil, nil, fmt.Errorf("%s needs its credentials", p.Name)
	}
	return vars, secrets, nil
}
