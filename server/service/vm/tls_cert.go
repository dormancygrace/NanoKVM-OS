package vm

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/config"
	"NanoKVM-Server/internal/atomicfile"
	"NanoKVM-Server/proto"
	"NanoKVM-Server/utils"
)

// A chain of a few certificates and a key fit in a few KiB.
const maxCertificateUpload = 256 * 1024

var (
	tlsCertMu           sync.Mutex
	stageCustomTLSFile  = prepareCustomTLSFile
	commitCustomTLSFile = func(staged, path string) error {
		if err := os.Rename(staged, path); err != nil {
			return err
		}
		return atomicfile.SyncDir(filepath.Dir(path))
	}

	// The uploaded pair; the configuration points here while a custom
	// certificate is in use, the generated pair stays for the way back.
	customCertPath    = config.CustomCertFile
	customKeyPath     = config.CustomKeyFile
	acmeCertPath      = config.AcmeCertFile
	acmeKeyPath       = config.AcmeKeyFile
	defaultCertPath   = config.DefaultCertFile
	defaultKeyPath    = config.DefaultKeyFile
	readTLSConfig     = config.Read
	updateTLS         = config.UpdateTLS
	serverCertificate = utils.ServerCertificate
	ensureGenerated   = func() error { return utils.EnsureServerCertificate(defaultCertPath, defaultKeyPath) }
	now               = time.Now
)

// GetTls reports HTTPS and the certificate in use.
func (s *Service) GetTls(c *gin.Context) {
	var rsp proto.Response
	conf, err := readTLSConfig()
	if err != nil {
		rsp.ErrRsp(c, -1, "cannot read the configuration")
		return
	}
	rsp.OkRspWithData(c, tlsStatus(conf))
}

// tlsStatus describes the configured pair and, with HTTPS on, the served one.
func tlsStatus(conf *config.Config) proto.TLSStatusRsp {
	cert := conf.Cert.WithDefaults()
	status := proto.TLSStatusRsp{
		Enabled:  conf.Proto == "https",
		Custom:   cert.Crt != defaultCertPath || cert.Key != defaultKeyPath,
		Source:   "custom",
		CertFile: cert.Crt,
		KeyFile:  cert.Key,
	}
	if !status.Custom {
		status.Source = "generated"
	} else if cert.Crt == acmeCertPath && cert.Key == acmeKeyPath {
		status.Source = "acme"
	}
	pair, err := utils.LoadCertificatePair(cert.Crt, cert.Key)
	if err != nil {
		status.Error = err.Error()
	} else if info, err := utils.DescribeCertificate(&pair); err != nil {
		status.Error = err.Error()
	} else {
		status.Certificate = &info
	}
	if !status.Enabled {
		return status
	}
	served := serverCertificate.Current()
	if served.Cert != nil {
		if info, err := utils.DescribeCertificate(served.Cert); err == nil {
			status.Served = &info
		}
	}
	if served.FallbackErr != nil && status.Error == "" {
		status.Error = served.FallbackErr.Error()
	}
	// The configured pair is not the served one: a reload (or a restart) is
	// due, or the generated pair stands in for a broken custom one.
	status.Pending = status.Certificate != nil && (status.Served == nil || status.Served.Fingerprint != status.Certificate.Fingerprint)
	return status
}

// UploadTlsCertificate stores a custom pair and, with HTTPS on, serves it to
// new connections at once. Form fields: "cert" (PEM, the chain leaf first,
// may include the key) and "key" (PEM; optional when "cert" holds it).
func (s *Service) UploadTlsCertificate(c *gin.Context) {
	var rsp proto.Response
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxCertificateUpload)
	certPEM, err := formFileBytes(c, "cert")
	if err != nil {
		rsp.ErrRsp(c, -1, "upload the certificate file (PEM)")
		return
	}
	keyPEM, err := formFileBytes(c, "key")
	if errors.Is(err, http.ErrMissingFile) {
		keyPEM = certPEM
	} else if err != nil {
		rsp.ErrRsp(c, -1, "cannot read the private key file")
		return
	}

	tlsCertMu.Lock()
	defer tlsCertMu.Unlock()

	pair, certOut, keyOut, err := utils.ParseCertificatePair(certPEM, keyPEM)
	if err != nil {
		rsp.ErrRsp(c, -1, err.Error())
		return
	}
	if t := now(); t.After(pair.Leaf.NotAfter) {
		rsp.ErrRsp(c, -1, fmt.Sprintf("the certificate expired on %s", pair.Leaf.NotAfter.UTC().Format("2006-01-02")))
		return
	} else if t.Before(pair.Leaf.NotBefore) {
		rsp.ErrRsp(c, -1, fmt.Sprintf("the certificate is not valid before %s", pair.Leaf.NotBefore.UTC().Format("2006-01-02")))
		return
	}

	conf, err := readTLSConfig()
	if err != nil {
		rsp.ErrRsp(c, -2, "cannot read the configuration")
		return
	}
	restore, err := storeCustomPair(certOut, keyOut)
	if err != nil {
		log.Errorf("failed to store the custom TLS certificate: %v", err)
		rsp.ErrRsp(c, -2, "cannot store the certificate")
		return
	}
	conf.Cert = config.Cert{Crt: customCertPath, Key: customKeyPath}
	if err := updateTLS(conf.Proto, conf.Cert); err != nil {
		// The reply says nothing was installed: put the previous files back,
		// or a configuration that already names the custom pair would serve
		// the refused one after a restart.
		if restoreErr := restore(); restoreErr != nil {
			err = errors.Join(err, restoreErr)
		}
		log.Errorf("failed to point the configuration at the custom TLS certificate: %v", err)
		rsp.ErrRsp(c, -2, "cannot update the configuration")
		return
	}
	if conf.Proto == "https" {
		if err := serverCertificate.Load(customCertPath, customKeyPath); err != nil {
			log.Errorf("failed to activate the custom TLS certificate: %v", err)
			rsp.ErrRsp(c, -3, "the certificate is stored but could not be activated: "+err.Error())
			return
		}
	}
	log.Infof("custom TLS certificate installed: %s", pair.Leaf.Subject.CommonName)
	rsp.OkRspWithData(c, tlsStatus(conf))
}

// storeCustomPair stages and syncs both files before replacing either one: a
// full filesystem must not leave an old certificate paired with a new key.
// Keep the previous bytes in memory for best-effort recovery of a commit error.
// This handles reported I/O failures; two renames are not a power-loss transaction.
// On success it returns restore, which puts the previous files back when a
// later step of the upload fails.
func storeCustomPair(certPEM, keyPEM []byte) (restore func() error, err error) {
	if err := os.MkdirAll(filepath.Dir(customKeyPath), 0o700); err != nil {
		return nil, err
	}
	files := []struct {
		path, staged   string
		data, previous []byte
		mode           os.FileMode
		existed        bool
	}{
		{path: customKeyPath, data: keyPEM, mode: 0o600},
		{path: customCertPath, data: certPEM, mode: 0o644},
	}
	defer func() {
		for _, file := range files {
			if file.staged != "" {
				_ = os.Remove(file.staged)
			}
		}
	}()
	for i := range files {
		old, err := os.ReadFile(files[i].path)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("read previous TLS file %s: %w", files[i].path, err)
		}
		files[i].previous, files[i].existed = old, err == nil
	}
	// restoreUpTo puts back the previous bytes (or absence) of files[0..last].
	restoreUpTo := func(last int) error {
		var errs []error
		for j := last; j >= 0; j-- {
			old := files[j]
			var restoreErr error
			if old.existed {
				restoreErr = atomicfile.Write(old.path, old.previous, old.mode)
			} else {
				restoreErr = os.Remove(old.path)
				if os.IsNotExist(restoreErr) {
					restoreErr = nil
				}
			}
			if restoreErr != nil {
				errs = append(errs, fmt.Errorf("restore previous TLS file %s: %w", old.path, restoreErr))
			}
		}
		return errors.Join(errs...)
	}
	for i := range files {
		staged, err := stageCustomTLSFile(files[i].path, files[i].data, files[i].mode)
		if err != nil {
			return nil, fmt.Errorf("stage TLS file %s: %w", files[i].path, err)
		}
		files[i].staged = staged
	}
	for i, file := range files {
		if err := commitCustomTLSFile(file.staged, file.path); err != nil {
			// A directory-sync failure may be reported after rename committed:
			// restore the failed write as well as every earlier replacement.
			return nil, errors.Join(fmt.Errorf("write TLS file %s: %w", file.path, err), restoreUpTo(i))
		}
	}
	return func() error { return restoreUpTo(len(files) - 1) }, nil
}

// prepareCustomTLSFile leaves a complete, synced replacement beside its target.
// The caller removes it after commit or failure; it is not a retained backup.
func prepareCustomTLSFile(path string, data []byte, mode os.FileMode) (name string, err error) {
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".pending-")
	if err != nil {
		return "", err
	}
	name = file.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(name)
		}
	}()
	if err = file.Chmod(mode); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return name, err
}

// DeleteTlsCertificate returns to the generated pair and removes the
// uploaded files. With HTTPS on, the generated pair is served before the
// custom files go, so the listener never runs out of a certificate.
func (s *Service) DeleteTlsCertificate(c *gin.Context) {
	var rsp proto.Response
	tlsCertMu.Lock()
	defer tlsCertMu.Unlock()

	conf, err := readTLSConfig()
	if err != nil {
		rsp.ErrRsp(c, -2, "cannot read the configuration")
		return
	}
	if conf.Proto == "https" {
		if err := ensureGenerated(); err != nil {
			log.Errorf("failed to prepare the generated TLS certificate: %v", err)
			rsp.ErrRsp(c, -2, "cannot prepare the generated certificate")
			return
		}
		if err := serverCertificate.Load(defaultCertPath, defaultKeyPath); err != nil {
			log.Errorf("failed to activate the generated TLS certificate: %v", err)
			rsp.ErrRsp(c, -2, "cannot activate the generated certificate")
			return
		}
	}
	conf.Cert = config.Cert{Crt: defaultCertPath, Key: defaultKeyPath}
	if err := updateTLS(conf.Proto, conf.Cert); err != nil {
		log.Errorf("failed to point the configuration at the generated TLS certificate: %v", err)
		rsp.ErrRsp(c, -2, "cannot update the configuration")
		return
	}
	for _, path := range []string{customKeyPath, customCertPath} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			log.Errorf("failed to remove %s: %v", path, err)
		}
	}
	log.Info("custom TLS certificate removed; the generated one is in use")
	rsp.OkRspWithData(c, tlsStatus(conf))
}

// ReloadTls serves the configured pair as it is on disk now, for a renewed
// certificate written by another tool.
func (s *Service) ReloadTls(c *gin.Context) {
	var rsp proto.Response
	tlsCertMu.Lock()
	defer tlsCertMu.Unlock()

	conf, err := readTLSConfig()
	if err != nil {
		rsp.ErrRsp(c, -2, "cannot read the configuration")
		return
	}
	if conf.Proto != "https" {
		rsp.ErrRsp(c, -1, "HTTPS is off")
		return
	}
	cert := conf.Cert.WithDefaults()
	if err := serverCertificate.Load(cert.Crt, cert.Key); err != nil {
		log.Errorf("failed to reload the TLS certificate: %v", err)
		rsp.ErrRsp(c, -3, err.Error())
		return
	}
	rsp.OkRspWithData(c, tlsStatus(conf))
}

// formFileBytes reads one uploaded file; http.ErrMissingFile when absent.
func formFileBytes(c *gin.Context, field string) ([]byte, error) {
	file, _, err := c.Request.FormFile(field)
	if err != nil {
		return nil, err
	}
	defer func(file multipart.File) { _ = file.Close() }(file)
	data, err := io.ReadAll(io.LimitReader(file, maxCertificateUpload+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxCertificateUpload {
		return nil, errors.New("file too large")
	}
	return data, nil
}
