package utils

import (
	"crypto/rand"
	"crypto/rsa"
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
)

// EnsureServerCertificate creates a device-specific certificate on first boot.
// Keep existing certificates and explicitly configured custom paths intact.
func EnsureServerCertificate(certFile, keyFile string) error {
	if _, err := tls.LoadX509KeyPair(certFile, keyFile); err == nil {
		return nil
	}
	if certFile != "/etc/kvm/server.crt" || keyFile != "/etc/kvm/server.key" {
		return fmt.Errorf("custom TLS certificate is missing or invalid")
	}
	return ensureGeneratedCertificate(certFile, keyFile)
}

func ensureGeneratedCertificate(certFile, keyFile string) error {
	if _, err := tls.LoadX509KeyPair(certFile, keyFile); err == nil {
		return nil
	}
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

func GenerateCert() error {
	return generateCertFiles("/etc/kvm/server.crt", "/etc/kvm/server.key")
}

func generateCertFiles(certFile, keyFile string) error {
	var (
		host      = "localhost"
		ipAddress = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
		validFor  = time.Hour * 24 * 365 * 10
	)

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Errorf("failed to generate RSA private key: %v", err)
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
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
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

	// generate certificate
	certOut, err := os.Create(certFile)
	if err != nil {
		log.Errorf("failed to create %s: %v", certFile, err)
		return err
	}

	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		log.Errorf("failed to encode %s: %v", certFile, err)
		return err
	}

	_ = certOut.Sync()
	_ = certOut.Close()
	log.Debugf("%s generated", certFile)

	// generate private key
	keyOut, err := os.OpenFile(keyFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600) // 权限 0600
	if err != nil {
		log.Errorf("failed to create %s: %v", keyFile, err)
		return err
	}

	privateBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		log.Errorf("failed to marshal private key: %v", err)
		return err
	}

	if err := pem.Encode(keyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: privateBytes}); err != nil {
		log.Errorf("failed to encode %s: %v", keyFile, err)
		return err
	}

	_ = keyOut.Sync()
	_ = keyOut.Close()
	log.Debugf("%s generated", keyFile)

	return nil
}
