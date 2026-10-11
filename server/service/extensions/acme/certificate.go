package acme

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/config"
	"NanoKVM-Server/utils"
)

// Hooks into the TLS configuration; variables for the tests.
var (
	readConfig        = config.Read
	updateTLS         = config.UpdateTLS
	serverCertificate = func() *utils.CertificateStore { return utils.ServerCertificate }
	ensureGenerated   = func() error { return utils.EnsureServerCertificate(defaultCertFile, defaultKeyFile) }
	defaultCertFile   = config.DefaultCertFile
	defaultKeyFile    = config.DefaultKeyFile
)

// inUse reports whether the configuration names the ACME pair.
func inUse(conf *config.Config) bool {
	cert := conf.Cert.WithDefaults()
	return cert.Crt == certFile && cert.Key == keyFile
}

// installCertArgs copies the issued pair to the TLS directory; acme.sh
// repeats this after every renewal.
func installCertArgs(primary string) []string {
	return []string{"--install-cert", "-d", primary, "--ecc", "--fullchain-file", certFile, "--key-file", keyFile}
}

// activate points the configuration at the ACME pair and serves it.
func activate() error {
	conf, err := readConfig()
	if err != nil {
		return err
	}
	if !inUse(conf) {
		if err := updateTLS(conf.Proto, config.Cert{Crt: certFile, Key: keyFile}); err != nil {
			return err
		}
	}
	if conf.Proto == "https" {
		return serverCertificate().Load(certFile, keyFile)
	}
	return nil
}

// reloadIfInUse serves the pair on disk after a renewal.
func reloadIfInUse() error {
	conf, err := readConfig()
	if err != nil {
		return fmt.Errorf("the renewed certificate could not be checked: %w", err)
	}
	if conf.Proto != "https" || !inUse(conf) {
		return nil
	}
	served := serverCertificate().Current()
	if served.Cert != nil {
		if pair, err := utils.LoadCertificatePair(certFile, keyFile); err == nil &&
			len(pair.Certificate) > 0 && len(served.Cert.Certificate) > 0 &&
			string(pair.Certificate[0]) == string(served.Cert.Certificate[0]) {
			return nil
		}
	}
	if err := serverCertificate().Load(certFile, keyFile); err != nil {
		return fmt.Errorf("the renewed certificate could not be put in use: %w", err)
	}
	log.Info("renewed ACME certificate in use")
	return nil
}

// retire makes acme.sh forget a domain record and deletes its keys: the
// record of a replaced certificate (every record installs to the same
// acme.crt/acme.key, so a stale one would overwrite the pair in use when it
// renews) and the record a failed issue for a new name leaves behind.
func retire(primary string) {
	if primary == "" || !domainRe.MatchString(primary) {
		return
	}
	if out, err := runSync(nil, "--remove", "-d", primary, "--ecc"); err != nil {
		log.Warnf("acme.sh --remove %s: %v: %s", primary, err, strings.TrimSpace(out))
	}
	if err := os.RemoveAll(filepath.Join(dataDir(), primary+"_ecc")); err != nil {
		log.Errorf("failed to remove the acme.sh record of %s: %v", primary, err)
	}
}

// deactivate returns to the generated pair when the ACME one is in use.
func deactivate() error {
	conf, err := readConfig()
	if err != nil {
		return err
	}
	if !inUse(conf) {
		return nil
	}
	if conf.Proto == "https" {
		if err := ensureGenerated(); err != nil {
			return err
		}
		if err := serverCertificate().Load(defaultCertFile, defaultKeyFile); err != nil {
			return err
		}
	}
	return updateTLS(conf.Proto, config.Cert{Crt: defaultCertFile, Key: defaultKeyFile})
}

func removePair() {
	for _, path := range []string{keyFile, certFile} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			log.Errorf("failed to remove %s: %v", path, err)
		}
	}
}

// nextRenewal reads Le_NextRenewTime from the acme.sh domain configuration.
func nextRenewal(primary string) string {
	if primary == "" {
		return ""
	}
	f, err := os.Open(filepath.Join(dataDir(), primary+"_ecc", primary+".conf"))
	if err != nil {
		return ""
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "Le_NextRenewTime=") {
			continue
		}
		value := strings.Trim(strings.TrimPrefix(line, "Le_NextRenewTime="), "'\"")
		if secs, err := strconv.ParseInt(value, 10, 64); err == nil && secs > 0 {
			return time.Unix(secs, 0).UTC().Format(time.RFC3339)
		}
	}
	return ""
}
