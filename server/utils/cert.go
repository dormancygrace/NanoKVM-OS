package utils

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/config"
	"NanoKVM-Server/internal/atomicfile"
)

// EnsureServerCertificate creates a device-specific certificate on first boot.
// Keep existing certificates and explicitly configured custom paths intact.
func EnsureServerCertificate(certFile, keyFile string) error {
	if _, err := tls.LoadX509KeyPair(certFile, keyFile); err == nil {
		return nil
	}
	if certFile != defaultCertFile || keyFile != defaultKeyFile {
		return fmt.Errorf("custom TLS certificate is missing or invalid")
	}
	return ensureGeneratedCertificate(certFile, keyFile)
}

// PrepareServerCertificate loads the configured pair into ServerCertificate
// for the HTTPS listener. A custom pair that cannot be used is logged and the
// generated pair stands in for this run, so a bad file never leaves the
// device unreachable; the configuration keeps naming the custom pair and the
// settings page shows the error.
func PrepareServerCertificate(certFile, keyFile string) error {
	if certFile == defaultCertFile && keyFile == defaultKeyFile {
		if err := ensureGeneratedCertificate(certFile, keyFile); err != nil {
			return err
		}
		return ServerCertificate.Load(certFile, keyFile)
	}
	customErr := ServerCertificate.Load(certFile, keyFile)
	if customErr == nil {
		return nil
	}
	log.Errorf("the configured HTTPS certificate %s cannot be used (%v); serving the generated certificate instead", certFile, customErr)
	if err := ensureGeneratedCertificate(defaultCertFile, defaultKeyFile); err != nil {
		return err
	}
	if err := ServerCertificate.Load(defaultCertFile, defaultKeyFile); err != nil {
		return err
	}
	ServerCertificate.MarkFallback(fmt.Errorf("%s cannot be used (%v); the generated certificate is served", certFile, customErr))
	return nil
}

func ensureGeneratedCertificate(certFile, keyFile string) error {
	if _, err := tls.LoadX509KeyPair(certFile, keyFile); err == nil {
		return nil
	}
	// Only a complete pair counts as existing: generation publishes the key
	// first, so a lone key is an interrupted first boot and is regenerated.
	_, certErr := os.Stat(certFile)
	_, keyErr := os.Stat(keyFile)
	if !os.IsNotExist(certErr) && !os.IsNotExist(keyErr) {
		return fmt.Errorf("existing TLS certificate cannot be loaded")
	}
	if err := os.MkdirAll(filepath.Dir(certFile), 0755); err != nil {
		return err
	}
	return generateCertFiles(certFile, keyFile)
}

// GenerateCertificateFiles writes a fresh self-signed pair.
func GenerateCertificateFiles(certFile, keyFile string) error {
	return generateCertFiles(certFile, keyFile)
}

// The generated pair; variables so tests can point them at a directory.
var (
	defaultCertFile = config.DefaultCertFile
	defaultKeyFile  = config.DefaultKeyFile
)

func generateCertFiles(certFile, keyFile string) error {
	var (
		host      = "localhost"
		ipAddress = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
		validFor  = time.Hour * 24 * 365 * 10
	)

	// ECDSA P-256: on the device an RSA-2048 signature takes about 80 ms of
	// every full TLS handshake and generating the key about 6.5 s at first
	// boot; P-256 takes about 3.6 ms and 10 ms. Existing certificates, RSA
	// included, are kept so browsers that trust them see no new warning.
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Errorf("failed to generate ECDSA private key: %v", err)
		return err
	}
	publicKey := &privateKey.PublicKey

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		log.Errorf("failed to generate serial number: %v", err)
		return err
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: host,
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(validFor),
		KeyUsage:              x509.KeyUsageDigitalSignature, // ECDSA keys only sign
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
		DNSNames:              []string{host},
		IPAddresses:           ipAddress,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, publicKey, privateKey)
	if err != nil {
		log.Errorf("failed to create certificate: %v", err)
		return err
	}

	privateBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		log.Errorf("failed to marshal private key: %v", err)
		return err
	}

	// Publish the key before the certificate, each atomically. A power cut
	// can then leave the pair incomplete (regenerated on the next start) but
	// never torn; a torn pair is kept as "existing" and stops HTTPS start-up.
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateBytes})
	if err := atomicfile.Write(keyFile, keyPEM, 0600); err != nil {
		log.Errorf("failed to write %s: %v", keyFile, err)
		return err
	}
	log.Debugf("%s generated", keyFile)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	if err := atomicfile.Write(certFile, certPEM, 0644); err != nil {
		log.Errorf("failed to write %s: %v", certFile, err)
		return err
	}
	log.Debugf("%s generated", certFile)

	return nil
}
