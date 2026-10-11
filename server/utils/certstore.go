package utils

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// ServerCertificate is the pair the HTTPS listener presents. It is swapped
// in place, so a new pair applies to new connections without a restart.
var ServerCertificate = &CertificateStore{}

// CertificateStore keeps the loaded pair for tls.Config.GetCertificate. A
// failed Load keeps the previous pair and records the error.
type CertificateStore struct {
	mu          sync.RWMutex
	cert        *tls.Certificate
	certFile    string
	keyFile     string
	loadedAt    time.Time
	err         error // the last Load that failed, cleared by a successful one
	fallbackErr error // why the configured pair was replaced by the generated one
}

// ServedCertificate is a snapshot of the store.
type ServedCertificate struct {
	Cert     *tls.Certificate
	CertFile string
	KeyFile  string
	LoadedAt time.Time
	// Err is the error of the last failed Load, nil after a successful one.
	Err error
	// FallbackErr is set while the generated pair stands in for a configured
	// pair that could not be used at start-up.
	FallbackErr error
}

// Load reads and checks the pair and makes it the served one.
func (s *CertificateStore) Load(certFile, keyFile string) error {
	cert, err := LoadCertificatePair(certFile, keyFile)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.err = err
		return err
	}
	s.cert = &cert
	s.certFile, s.keyFile = certFile, keyFile
	s.loadedAt = time.Now()
	s.err = nil
	s.fallbackErr = nil
	return nil
}

// MarkFallback records that the served pair stands in for the configured one.
func (s *CertificateStore) MarkFallback(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fallbackErr = err
}

// GetCertificate is the tls.Config callback.
func (s *CertificateStore) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cert == nil {
		return nil, errors.New("no server certificate loaded")
	}
	return s.cert, nil
}

// Current returns a snapshot of the store.
func (s *CertificateStore) Current() ServedCertificate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return ServedCertificate{Cert: s.cert, CertFile: s.certFile, KeyFile: s.keyFile, LoadedAt: s.loadedAt, Err: s.err, FallbackErr: s.fallbackErr}
}

// LoadCertificatePair reads and checks a pair from disk.
func LoadCertificatePair(certFile, keyFile string) (tls.Certificate, error) {
	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("read certificate: %w", err)
	}
	keyPEM, err := os.ReadFile(keyFile)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("read private key: %w", err)
	}
	cert, _, _, err := ParseCertificatePair(certPEM, keyPEM)
	return cert, err
}

// maxChainLength bounds the certificates accepted in one upload; a server
// chain is the leaf plus a few intermediates.
const maxChainLength = 8

// ParseCertificatePair checks an uploaded pair. The certificate input may
// hold the chain, leaf first, and may also hold the key (a combined PEM);
// the key input may hold the certificates too. The returned PEM texts hold
// only the certificates and only the key, so the stored certificate file
// never contains the key. Errors are meant for the settings page.
func ParseCertificatePair(certPEM, keyPEM []byte) (cert tls.Certificate, certOut, keyOut []byte, err error) {
	certOut, err = certificateBlocks(certPEM)
	if err != nil {
		return tls.Certificate{}, nil, nil, err
	}
	keyOut, err = privateKeyBlock(keyPEM)
	if err != nil {
		return tls.Certificate{}, nil, nil, err
	}
	cert, err = tls.X509KeyPair(certOut, keyOut)
	if err != nil {
		return tls.Certificate{}, nil, nil, errors.New(strings.TrimPrefix(err.Error(), "tls: "))
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("parse certificate: %w", err)
	}
	cert.Leaf = leaf
	return cert, certOut, keyOut, nil
}

func certificateBlocks(data []byte) ([]byte, error) {
	var out bytes.Buffer
	count := 0
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		count++
		if count > maxChainLength {
			return nil, fmt.Errorf("more than %d certificates in the upload", maxChainLength)
		}
		if err := pem.Encode(&out, &pem.Block{Type: block.Type, Bytes: block.Bytes}); err != nil {
			return nil, err
		}
	}
	if count == 0 {
		return nil, errors.New("no certificate found: upload a PEM file with a CERTIFICATE block")
	}
	return out.Bytes(), nil
}

func privateKeyBlock(data []byte) ([]byte, error) {
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "ENCRYPTED PRIVATE KEY" || strings.Contains(block.Headers["Proc-Type"], "ENCRYPTED") {
			return nil, errors.New("the private key is password-protected: upload an unencrypted key")
		}
		if block.Type == "PRIVATE KEY" || strings.HasSuffix(block.Type, " PRIVATE KEY") {
			return pem.EncodeToMemory(&pem.Block{Type: block.Type, Bytes: block.Bytes}), nil
		}
	}
	return nil, errors.New("no private key found: upload a PEM file with a PRIVATE KEY block")
}

// CertificateInfo describes a certificate for the settings page.
type CertificateInfo struct {
	Subject     string   `json:"subject"`
	Issuer      string   `json:"issuer"`
	DNSNames    []string `json:"dnsNames"`
	IPAddresses []string `json:"ipAddresses"`
	NotBefore   string   `json:"notBefore"`
	NotAfter    string   `json:"notAfter"`
	Expired     bool     `json:"expired"`
	// Fingerprint is the SHA-256 of the leaf in DER, hex with colons, the
	// form browsers show.
	Fingerprint string `json:"fingerprint"`
	KeyType     string `json:"keyType"`
	SelfSigned  bool   `json:"selfSigned"`
	// Chain is the number of certificates in the file, the leaf included.
	Chain int `json:"chain"`
}

// DescribeCertificate summarises the leaf of a loaded pair.
func DescribeCertificate(cert *tls.Certificate) (CertificateInfo, error) {
	if cert == nil || len(cert.Certificate) == 0 {
		return CertificateInfo{}, errors.New("no certificate")
	}
	leaf := cert.Leaf
	if leaf == nil {
		var err error
		if leaf, err = x509.ParseCertificate(cert.Certificate[0]); err != nil {
			return CertificateInfo{}, err
		}
	}
	sum := sha256.Sum256(leaf.Raw)
	info := CertificateInfo{
		Subject:     nameString(leaf.Subject.CommonName, leaf.Subject.String()),
		Issuer:      nameString(leaf.Issuer.CommonName, leaf.Issuer.String()),
		DNSNames:    append([]string{}, leaf.DNSNames...),
		IPAddresses: make([]string, 0, len(leaf.IPAddresses)),
		NotBefore:   leaf.NotBefore.UTC().Format(time.RFC3339),
		NotAfter:    leaf.NotAfter.UTC().Format(time.RFC3339),
		Expired:     time.Now().After(leaf.NotAfter),
		Fingerprint: fingerprint(sum[:]),
		KeyType:     keyType(leaf.PublicKey),
		Chain:       len(cert.Certificate),
	}
	for _, ip := range leaf.IPAddresses {
		info.IPAddresses = append(info.IPAddresses, ip.String())
	}
	if bytes.Equal(leaf.RawIssuer, leaf.RawSubject) {
		info.SelfSigned = leaf.CheckSignature(leaf.SignatureAlgorithm, leaf.RawTBSCertificate, leaf.Signature) == nil
	}
	return info, nil
}

func nameString(commonName, full string) string {
	if commonName != "" {
		return commonName
	}
	return full
}

func fingerprint(sum []byte) string {
	h := strings.ToUpper(hex.EncodeToString(sum))
	parts := make([]string, 0, len(h)/2)
	for i := 0; i+2 <= len(h); i += 2 {
		parts = append(parts, h[i:i+2])
	}
	return strings.Join(parts, ":")
}

func keyType(key any) string {
	switch k := key.(type) {
	case *ecdsa.PublicKey:
		return "ECDSA " + k.Curve.Params().Name
	case *rsa.PublicKey:
		return fmt.Sprintf("RSA %d", k.N.BitLen())
	case ed25519.PublicKey:
		return "Ed25519"
	default:
		return fmt.Sprintf("%T", key)
	}
}
