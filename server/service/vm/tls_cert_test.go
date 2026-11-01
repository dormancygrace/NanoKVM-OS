package vm

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"NanoKVM-Server/config"
	"NanoKVM-Server/proto"
	"NanoKVM-Server/utils"
)

func tlsTestPair(t *testing.T, name string, notBefore time.Time) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 64))
	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    notBefore,
		NotAfter:     notBefore.Add(365 * 24 * time.Hour),
		DNSNames:     []string{name},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}

// tlsFixture swaps the paths and the configuration for a temporary set-up.
type tlsFixture struct {
	t       *testing.T
	dir     string
	conf    *config.Config
	updates []config.Cert
	router  *gin.Engine
}

func newTLSFixture(t *testing.T, proto string) *tlsFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	f := &tlsFixture{t: t, dir: dir, conf: &config.Config{Proto: proto}}

	origCustomCert, origCustomKey := customCertPath, customKeyPath
	origDefaultCert, origDefaultKey := defaultCertPath, defaultKeyPath
	origRead, origUpdate, origStore, origEnsure, origNow := readTLSConfig, updateTLS, serverCertificate, ensureGenerated, now
	customCertPath, customKeyPath = filepath.Join(dir, "tls", "custom.crt"), filepath.Join(dir, "tls", "custom.key")
	defaultCertPath, defaultKeyPath = filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key")
	readTLSConfig = func() (*config.Config, error) {
		c := *f.conf
		return &c, nil
	}
	updateTLS = func(proto string, cert config.Cert) error {
		f.conf.Proto, f.conf.Cert = proto, cert
		f.updates = append(f.updates, cert)
		return nil
	}
	serverCertificate = &utils.CertificateStore{}
	ensureGenerated = func() error {
		if _, err := utils.LoadCertificatePair(defaultCertPath, defaultKeyPath); err == nil {
			return nil
		}
		return utils.GenerateCertificateFiles(defaultCertPath, defaultKeyPath)
	}
	now = time.Now
	t.Cleanup(func() {
		customCertPath, customKeyPath = origCustomCert, origCustomKey
		defaultCertPath, defaultKeyPath = origDefaultCert, origDefaultKey
		readTLSConfig, updateTLS, serverCertificate, ensureGenerated, now = origRead, origUpdate, origStore, origEnsure, origNow
	})

	// The generated pair exists and, with HTTPS on, is served.
	if err := utils.GenerateCertificateFiles(defaultCertPath, defaultKeyPath); err != nil {
		t.Fatal(err)
	}
	f.conf.Cert = config.Cert{Crt: defaultCertPath, Key: defaultKeyPath}
	if proto == "https" {
		if err := serverCertificate.Load(defaultCertPath, defaultKeyPath); err != nil {
			t.Fatal(err)
		}
	}

	s := &Service{}
	f.router = gin.New()
	f.router.GET("/tls", s.GetTls)
	f.router.POST("/tls/certificate", s.UploadTlsCertificate)
	f.router.POST("/tls/certificate/delete", s.DeleteTlsCertificate)
	f.router.POST("/tls/reload", s.ReloadTls)
	return f
}

type tlsResponse struct {
	Code int                `json:"code"`
	Msg  string             `json:"msg"`
	Data proto.TLSStatusRsp `json:"data"`
}

func (f *tlsFixture) do(method, path string, body *bytes.Buffer, contentType string) tlsResponse {
	f.t.Helper()
	if body == nil {
		body = &bytes.Buffer{}
	}
	req := httptest.NewRequest(method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	var rsp tlsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &rsp); err != nil {
		f.t.Fatalf("response %d: %s", rec.Code, rec.Body.String())
	}
	return rsp
}

func (f *tlsFixture) upload(files map[string][]byte) tlsResponse {
	f.t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for field, data := range files {
		part, err := w.CreateFormFile(field, field+".pem")
		if err != nil {
			f.t.Fatal(err)
		}
		_, _ = part.Write(data)
	}
	_ = w.Close()
	return f.do(http.MethodPost, "/tls/certificate", &body, w.FormDataContentType())
}

func (f *tlsFixture) servedName() string {
	f.t.Helper()
	cert, err := serverCertificate.GetCertificate(nil)
	if err != nil {
		return ""
	}
	return cert.Leaf.Subject.CommonName
}

func TestUploadTlsCertificateServesTheNewPairAtOnce(t *testing.T) {
	f := newTLSFixture(t, "https")
	certPEM, keyPEM := tlsTestPair(t, "kvm.example", time.Now().Add(-time.Hour))

	rsp := f.upload(map[string][]byte{"cert": certPEM, "key": keyPEM})
	if rsp.Code != 0 {
		t.Fatalf("upload refused: %+v", rsp)
	}
	if f.servedName() != "kvm.example" {
		t.Fatalf("served %q after the upload", f.servedName())
	}
	if len(f.updates) != 1 || f.updates[0].Crt != customCertPath || f.updates[0].Key != customKeyPath || f.conf.Proto != "https" {
		t.Fatalf("configuration updates = %+v, proto %q", f.updates, f.conf.Proto)
	}
	certInfo, err := os.Stat(customCertPath)
	if err != nil {
		t.Fatal(err)
	}
	keyInfo, err := os.Stat(customKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if certInfo.Mode().Perm() != 0o644 || keyInfo.Mode().Perm() != 0o600 {
		t.Fatalf("modes: cert %v key %v", certInfo.Mode().Perm(), keyInfo.Mode().Perm())
	}
	if !rsp.Data.Enabled || !rsp.Data.Custom || rsp.Data.Pending || rsp.Data.Error != "" {
		t.Fatalf("status after the upload: %+v", rsp.Data)
	}
	if rsp.Data.Certificate == nil || rsp.Data.Served == nil || rsp.Data.Certificate.Fingerprint != rsp.Data.Served.Fingerprint {
		t.Fatalf("status pairs differ: %+v", rsp.Data)
	}
	if rsp.Data.Certificate.Subject != "kvm.example" || !rsp.Data.Certificate.SelfSigned {
		t.Fatalf("certificate = %+v", rsp.Data.Certificate)
	}
}

func TestUploadTlsCertificateAcceptsACombinedFileAndSeparatesTheKey(t *testing.T) {
	f := newTLSFixture(t, "https")
	certPEM, keyPEM := tlsTestPair(t, "combined", time.Now().Add(-time.Hour))
	rsp := f.upload(map[string][]byte{"cert": append(append([]byte{}, certPEM...), keyPEM...)})
	if rsp.Code != 0 {
		t.Fatalf("combined upload refused: %+v", rsp)
	}
	stored, err := os.ReadFile(customCertPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), "PRIVATE KEY") {
		t.Fatal("the stored certificate file holds the key")
	}
	if f.servedName() != "combined" {
		t.Fatalf("served %q", f.servedName())
	}
}

func TestUploadTlsCertificateRefusesBadPairsWithoutChangingAnything(t *testing.T) {
	f := newTLSFixture(t, "https")
	certPEM, _ := tlsTestPair(t, "a", time.Now().Add(-time.Hour))
	_, otherKey := tlsTestPair(t, "b", time.Now().Add(-time.Hour))
	expiredCert, expiredKey := tlsTestPair(t, "old", time.Now().Add(-2*365*24*time.Hour))
	futureCert, futureKey := tlsTestPair(t, "soon", time.Now().Add(48*time.Hour))
	cases := []struct {
		name  string
		files map[string][]byte
		want  string
	}{
		{"mismatch", map[string][]byte{"cert": certPEM, "key": otherKey}, "does not match"},
		{"no certificate", map[string][]byte{"cert": otherKey}, "no certificate"},
		{"no key", map[string][]byte{"cert": certPEM}, "no private key"},
		{"expired", map[string][]byte{"cert": expiredCert, "key": expiredKey}, "expired on"},
		{"not yet valid", map[string][]byte{"cert": futureCert, "key": futureKey}, "not valid before"},
		{"no file", map[string][]byte{}, "upload the certificate"},
	}
	for _, tc := range cases {
		rsp := f.upload(tc.files)
		if rsp.Code != -1 || !strings.Contains(rsp.Msg, tc.want) {
			t.Errorf("%s: code %d msg %q, want %q", tc.name, rsp.Code, rsp.Msg, tc.want)
		}
	}
	if len(f.updates) != 0 {
		t.Fatalf("configuration changed by a refused upload: %+v", f.updates)
	}
	if _, err := os.Stat(customCertPath); !os.IsNotExist(err) {
		t.Fatalf("custom certificate written by a refused upload: %v", err)
	}
	if f.servedName() != "localhost" {
		t.Fatalf("served %q, want the generated pair", f.servedName())
	}
}

func TestUploadTlsCertificateWithHTTPSOffOnlyStoresThePair(t *testing.T) {
	f := newTLSFixture(t, "http")
	certPEM, keyPEM := tlsTestPair(t, "later", time.Now().Add(-time.Hour))
	rsp := f.upload(map[string][]byte{"cert": certPEM, "key": keyPEM})
	if rsp.Code != 0 {
		t.Fatalf("upload refused: %+v", rsp)
	}
	if rsp.Data.Enabled || !rsp.Data.Custom || rsp.Data.Served != nil || rsp.Data.Certificate == nil {
		t.Fatalf("status = %+v", rsp.Data)
	}
	if f.conf.Proto != "http" || f.conf.Cert.Crt != customCertPath {
		t.Fatalf("configuration = %+v", f.conf)
	}
	if _, err := serverCertificate.GetCertificate(nil); err == nil {
		t.Fatal("the store was loaded while HTTPS is off")
	}
}

func TestDeleteTlsCertificateReturnsToTheGeneratedPair(t *testing.T) {
	f := newTLSFixture(t, "https")
	certPEM, keyPEM := tlsTestPair(t, "custom", time.Now().Add(-time.Hour))
	if rsp := f.upload(map[string][]byte{"cert": certPEM, "key": keyPEM}); rsp.Code != 0 {
		t.Fatal(rsp)
	}
	rsp := f.do(http.MethodPost, "/tls/certificate/delete", nil, "")
	if rsp.Code != 0 {
		t.Fatalf("delete refused: %+v", rsp)
	}
	if f.servedName() != "localhost" {
		t.Fatalf("served %q after the delete", f.servedName())
	}
	if f.conf.Cert.Crt != defaultCertPath || f.conf.Cert.Key != defaultKeyPath {
		t.Fatalf("configuration = %+v", f.conf.Cert)
	}
	for _, path := range []string{customCertPath, customKeyPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s kept: %v", path, err)
		}
	}
	if rsp.Data.Custom || rsp.Data.Pending || rsp.Data.Certificate == nil || !rsp.Data.Certificate.SelfSigned {
		t.Fatalf("status = %+v", rsp.Data)
	}
	// Deleting again is harmless.
	if rsp := f.do(http.MethodPost, "/tls/certificate/delete", nil, ""); rsp.Code != 0 {
		t.Fatalf("second delete: %+v", rsp)
	}
}

func TestReloadTlsServesTheFilesOnDiskAndKeepsThePairOnFailure(t *testing.T) {
	f := newTLSFixture(t, "https")
	certPEM, keyPEM := tlsTestPair(t, "renewed", time.Now().Add(-time.Hour))
	// Another tool replaced the generated pair in place.
	if err := os.WriteFile(defaultCertPath, certPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defaultKeyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	status := f.do(http.MethodGet, "/tls", nil, "")
	if !status.Data.Pending || status.Data.Certificate.Subject != "renewed" || status.Data.Served.Subject != "localhost" {
		t.Fatalf("status before the reload: %+v", status.Data)
	}
	rsp := f.do(http.MethodPost, "/tls/reload", nil, "")
	if rsp.Code != 0 || f.servedName() != "renewed" || rsp.Data.Pending {
		t.Fatalf("reload: %+v served %q", rsp, f.servedName())
	}

	if err := os.WriteFile(defaultKeyPath, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	rsp = f.do(http.MethodPost, "/tls/reload", nil, "")
	if rsp.Code != -3 || f.servedName() != "renewed" {
		t.Fatalf("broken reload: %+v served %q", rsp, f.servedName())
	}
	status = f.do(http.MethodGet, "/tls", nil, "")
	if status.Data.Error == "" || status.Data.Certificate != nil || status.Data.Served == nil {
		t.Fatalf("status with a broken pair on disk: %+v", status.Data)
	}

	f.conf.Proto = "http"
	if rsp := f.do(http.MethodPost, "/tls/reload", nil, ""); rsp.Code != -1 {
		t.Fatalf("reload with HTTPS off: %+v", rsp)
	}
}

// A full filesystem can accept staging the new key, then reject the certificate.
// The reported failure must leave the old on-disk pair usable after restart.
func TestTLSStorageFailurePreservesExistingPair(t *testing.T) {
	f := newTLSFixture(t, "https")
	oldCert, oldKey := tlsTestPair(t, "old.example", time.Now().Add(-time.Hour))
	if rsp := f.upload(map[string][]byte{"cert": oldCert, "key": oldKey}); rsp.Code != 0 {
		t.Fatalf("initial upload: %+v", rsp)
	}
	writer := stageCustomTLSFile
	t.Cleanup(func() { stageCustomTLSFile = writer })
	stageCustomTLSFile = func(path string, data []byte, mode os.FileMode) (string, error) {
		if path == customCertPath {
			return "", syscall.ENOSPC
		}
		return writer(path, data, mode)
	}
	cert, key := tlsTestPair(t, "new.example", time.Now().Add(-time.Hour))
	if rsp := f.upload(map[string][]byte{"cert": cert, "key": key}); rsp.Code != -2 {
		t.Fatalf("storage failure was accepted: %+v", rsp)
	}
	pair, err := utils.LoadCertificatePair(customCertPath, customKeyPath)
	if err != nil {
		t.Fatalf("failed upload broke the previously installed pair: %v", err)
	}
	if pair.Leaf.Subject.CommonName != "old.example" {
		t.Fatalf("failed upload changed certificate: %s", pair.Leaf.Subject.CommonName)
	}
	if f.servedName() != "old.example" {
		t.Fatalf("served %q", f.servedName())
	}
}

// The pair is on disk, then the configuration cannot be written: the reply
// says nothing was installed, so the files must be what they were. Otherwise
// a configuration that already names the custom pair serves the refused one
// after a restart, and a first upload leaves its private key behind.
func TestTLSConfigurationFailureRestoresPreviousFiles(t *testing.T) {
	for _, replacing := range []bool{true, false} {
		t.Run(map[bool]string{true: "replacement", false: "first upload"}[replacing], func(t *testing.T) {
			f := newTLSFixture(t, "https")
			if replacing {
				cert, key := tlsTestPair(t, "old.example", time.Now().Add(-time.Hour))
				if rsp := f.upload(map[string][]byte{"cert": cert, "key": key}); rsp.Code != 0 {
					t.Fatal(rsp)
				}
			}
			served := f.servedName()
			update := updateTLS
			t.Cleanup(func() { updateTLS = update })
			updateTLS = func(string, config.Cert) error { return syscall.ENOSPC }
			cert, key := tlsTestPair(t, "new.example", time.Now().Add(-time.Hour))
			if rsp := f.upload(map[string][]byte{"cert": cert, "key": key}); rsp.Code != -2 {
				t.Fatalf("configuration failure was accepted: %+v", rsp)
			}
			if f.servedName() != served {
				t.Fatalf("served %q, want %q", f.servedName(), served)
			}
			if replacing {
				old, err := utils.LoadCertificatePair(customCertPath, customKeyPath)
				if err != nil || old.Leaf.Subject.CommonName != "old.example" {
					t.Fatalf("the refused upload replaced the installed pair: %v", err)
				}
				return
			}
			for _, path := range []string{customKeyPath, customCertPath} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("refused first upload left %s: %v", path, err)
				}
			}
		})
	}
}
