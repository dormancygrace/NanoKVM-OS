package acme

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/proto"
	"NanoKVM-Server/utils"
)

type Service struct{}

func NewService() *Service { return &Service{} }

// Status is what the settings page shows.
type Status struct {
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	// Latest is the newest stable release as of the last check (the page
	// asks with POST /check); UpdateAvailable compares it with Version.
	Latest          *Release `json:"latest,omitempty"`
	UpdateAvailable bool     `json:"updateAvailable"`
	// Settings of the last issue, without credentials.
	Settings *Settings `json:"settings,omitempty"`
	// Certificate describes the issued pair when it exists.
	Certificate *utils.CertificateInfo `json:"certificate,omitempty"`
	// InUse: the HTTPS configuration names the ACME pair.
	InUse       bool   `json:"inUse"`
	NextRenewal string `json:"nextRenewal,omitempty"`
	Running     bool   `json:"running"`
	LastRun     *Job   `json:"lastRun,omitempty"`
}

func status() Status {
	s := Status{Installed: Installed(), Latest: knownLatest(), Running: running(), LastRun: lastRun()}
	if s.Installed {
		s.Version = installedVersion()
		s.UpdateAvailable = s.Latest != nil && newerVersion(s.Version, s.Latest.Tag)
	}
	if settings, err := loadSettings(); err == nil {
		s.Settings = settings
		s.NextRenewal = nextRenewal(settings.primary())
	}
	if pair, err := utils.LoadCertificatePair(certFile, keyFile); err == nil {
		if info, err := utils.DescribeCertificate(&pair); err == nil {
			s.Certificate = &info
		}
	}
	if conf, err := readConfig(); err == nil {
		s.InUse = inUse(conf)
	}
	return s
}

func (s *Service) GetStatus(c *gin.Context) {
	var rsp proto.Response
	rsp.OkRspWithData(c, status())
}

// Install downloads the latest stable acme.sh release, as a first install
// or an update; the call lasts as long as the download.
func (s *Service) Install(c *gin.Context) {
	var rsp proto.Response
	if _, err := Install(c.Request.Context()); err != nil {
		log.Errorf("acme.sh install failed: %v", err)
		rsp.ErrRsp(c, -2, err.Error())
		return
	}
	rsp.OkRspWithData(c, status())
}

// CheckUpdate asks GitHub for the latest stable release.
func (s *Service) CheckUpdate(c *gin.Context) {
	var rsp proto.Response
	if _, err := latestRelease(c.Request.Context()); err != nil {
		rsp.ErrRsp(c, -2, err.Error())
		return
	}
	rsp.OkRspWithData(c, status())
}

// Uninstall removes acme.sh with its account, certificates and settings,
// after returning to the generated pair when the ACME one is in use.
func (s *Service) Uninstall(c *gin.Context) {
	var rsp proto.Response
	opMu.Lock()
	defer opMu.Unlock()
	if running() {
		rsp.ErrRsp(c, -1, "an acme.sh operation is running")
		return
	}
	if err := deactivate(); err != nil {
		log.Errorf("failed to return to the generated certificate: %v", err)
		rsp.ErrRsp(c, -2, "cannot return to the generated certificate")
		return
	}
	removePair()
	if err := os.RemoveAll(root); err != nil {
		log.Errorf("failed to remove %s: %v", root, err)
		rsp.ErrRsp(c, -2, "cannot remove the acme.sh directory")
		return
	}
	jobMu.Lock()
	lastJob = nil
	jobMu.Unlock()
	log.Info("acme.sh removed")
	rsp.OkRspWithData(c, status())
}

func (s *Service) GetProviders(c *gin.Context) {
	var rsp proto.Response
	if !Installed() {
		rsp.ErrRsp(c, -1, "acme.sh is not installed")
		return
	}
	providers, err := ListProviders()
	if err != nil {
		rsp.ErrRsp(c, -2, "cannot read the DNS hooks")
		return
	}
	rsp.OkRspWithData(c, providers)
}

// IssueReq carries the settings plus the one-time secrets.
type IssueReq struct {
	Settings
	// DNSEnv are the provider's variables, by the names its hook declares.
	DNSEnv map[string]string `json:"dnsEnv"`
	// External account binding, needed by Google.
	EABKid     string `json:"eabKid"`
	EABHmacKey string `json:"eabHmacKey"`
}

// Issue starts an acme.sh --issue followed by --install-cert; on success the
// pair is served. The page polls the status for the result.
func (s *Service) Issue(c *gin.Context) {
	var rsp proto.Response
	var req IssueReq
	if err := c.ShouldBindJSON(&req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}
	if !Installed() {
		rsp.ErrRsp(c, -1, "acme.sh is not installed")
		return
	}
	settings := req.Settings
	settings.IssuedAt = ""
	if err := settings.normalize(); err != nil {
		rsp.ErrRsp(c, -1, err.Error())
		return
	}
	args, env, secrets, err := issueArgs(settings, req)
	if err != nil {
		rsp.ErrRsp(c, -1, err.Error())
		return
	}
	if settings.Challenge == challengeHTTP {
		// The CA fetches the token on port 80 only.
		conf, err := readConfig()
		if err != nil {
			rsp.ErrRsp(c, -2, "cannot read the configuration")
			return
		}
		if conf.Port.Http != 80 {
			rsp.ErrRsp(c, -1, fmt.Sprintf("HTTP validation needs the plain-HTTP port to be 80 (it is %d)", conf.Port.Http))
			return
		}
	}
	opMu.Lock()
	defer opMu.Unlock()
	if settings.Challenge == challengeHTTP {
		EnsureChallengeDir()
	}
	if err := os.MkdirAll(certDir(), 0o700); err != nil {
		rsp.ErrRsp(c, -2, "cannot prepare the certificate directory")
		return
	}
	// The certificate in place, if any, keeps its settings (they name the
	// record renewals and Remove act on) until the new one is installed.
	previous := ""
	if saved, err := loadSettings(); err == nil {
		previous = saved.primary()
	}
	steps := []step{{args: args}, {args: installCertArgs(settings.primary())}}
	err = startJob("issue", env, secrets, steps, func(j *Job) error {
		if !j.OK {
			if settings.primary() != previous {
				retire(settings.primary())
			}
			return nil
		}
		// --install-cert has written the new pair: from here on the new
		// record owns acme.crt/acme.key.
		settings.IssuedAt = time.Now().UTC().Format(time.RFC3339)
		if err := saveSettings(&settings); err != nil {
			return fmt.Errorf("the certificate was issued but its settings could not be saved: %w", err)
		}
		if previous != "" && previous != settings.primary() {
			retire(previous)
		}
		if err := activate(); err != nil {
			return fmt.Errorf("the certificate was issued but could not be put in use: %w", err)
		}
		return nil
	})
	if err != nil {
		rsp.ErrRsp(c, -1, err.Error())
		return
	}
	rsp.OkRspWithData(c, status())
}

// issueArgs builds the acme.sh --issue command line. --force: acme.sh skips
// an issue for unchanged domains even when the CA changed, and the button
// is an explicit request.
func issueArgs(settings Settings, req IssueReq) (args, env, secrets []string, err error) {
	args = []string{"--issue", "--server", caServers[settings.CA], "--keylength", "ec-256", "--force"}
	for _, d := range settings.Domains {
		args = append(args, "-d", d)
	}
	if settings.Email != "" {
		args = append(args, "--accountemail", settings.Email)
	}
	switch settings.Challenge {
	case challengeHTTP:
		args = append(args, "--webroot", challengeRoot)
	case challengeDNS:
		p, ok := provider(settings.DNSProvider)
		if !ok {
			return nil, nil, nil, errors.New("the DNS provider is not installed")
		}
		env, secrets, err = checkEnv(p, req.DNSEnv)
		if err != nil {
			return nil, nil, nil, err
		}
		args = append(args, "--dns", settings.DNSProvider)
		if settings.DNSSleep > 0 {
			args = append(args, "--dnssleep", itoa(settings.DNSSleep))
		}
	}
	kid, hmac := strings.TrimSpace(req.EABKid), strings.TrimSpace(req.EABHmacKey)
	if settings.CA == "google" || settings.CA == "google_test" {
		if kid == "" || hmac == "" {
			return nil, nil, nil, errors.New("Google needs the EAB key ID and HMAC key")
		}
	}
	if kid != "" || hmac != "" {
		if kid == "" || hmac == "" || len(kid) > 256 || len(hmac) > 1024 || strings.ContainsAny(kid+hmac, " \t\r\n\x00") {
			return nil, nil, nil, errors.New("the EAB key ID and HMAC key are not usable")
		}
		args = append(args, "--eab-kid", kid, "--eab-hmac-key", hmac)
		secrets = append(secrets, hmac)
	}
	return args, env, secrets, nil
}

// Renew starts acme.sh --renew --force for the issued certificate; the
// provider credentials come from acme.sh's own account.conf.
func (s *Service) Renew(c *gin.Context) {
	var rsp proto.Response
	settings, err := loadSettings()
	if err != nil || !Installed() {
		rsp.ErrRsp(c, -1, "no certificate has been issued")
		return
	}
	opMu.Lock()
	defer opMu.Unlock()
	steps := []step{renewStep(settings, true)}
	err = startJob("renew", nil, nil, steps, func(j *Job) error {
		if !j.OK {
			return nil
		}
		return reloadIfInUse()
	})
	if err != nil {
		rsp.ErrRsp(c, -1, err.Error())
		return
	}
	rsp.OkRspWithData(c, status())
}

// Remove stops renewing, deletes the issued pair and returns to the
// generated certificate when the ACME one is in use. acme.sh and the
// account stay for the next issue.
func (s *Service) Remove(c *gin.Context) {
	var rsp proto.Response
	opMu.Lock()
	defer opMu.Unlock()
	if running() {
		rsp.ErrRsp(c, -1, "an acme.sh operation is running")
		return
	}
	if err := deactivate(); err != nil {
		log.Errorf("failed to return to the generated certificate: %v", err)
		rsp.ErrRsp(c, -2, "cannot return to the generated certificate")
		return
	}
	if settings, err := loadSettings(); err == nil && Installed() {
		retire(settings.primary())
	}
	removePair()
	if err := os.Remove(settingsFile()); err != nil && !os.IsNotExist(err) {
		log.Errorf("failed to remove the ACME settings: %v", err)
	}
	removeLog()
	jobMu.Lock()
	lastJob = nil
	jobMu.Unlock()
	rsp.OkRspWithData(c, status())
}
