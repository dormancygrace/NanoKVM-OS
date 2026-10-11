package acme

import (
	"bufio"
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
func reloadIfInUse() {
	conf, err := readConfig()
	if err != nil || conf.Proto != "https" || !inUse(conf) {
		return
	}
	served := serverCertificate().Current()
	if served.Cert != nil {
		if pair, err := utils.LoadCertificatePair(certFile, keyFile); err == nil &&
			len(pair.Certificate) > 0 && len(served.Cert.Certificate) > 0 &&
			string(pair.Certificate[0]) == string(served.Cert.Certificate[0]) {
			return
		}
	}
	if err := serverCertificate().Load(certFile, keyFile); err != nil {
		log.Errorf("failed to serve the renewed ACME certificate: %v", err)
		return
	}
	log.Info("renewed ACME certificate in use")
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
