package acme

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"NanoKVM-Server/config"
	"NanoKVM-Server/utils"
)

// useTempTree points the package at a temporary layout.
func useTempTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	origRoot, origChallenge, origCert, origKey := root, challengeRoot, certFile, keyFile
	root = filepath.Join(dir, "acme")
	challengeRoot = filepath.Join(dir, "challenge")
	certFile = filepath.Join(dir, "tls", "acme.crt")
	keyFile = filepath.Join(dir, "tls", "acme.key")
	t.Cleanup(func() {
		root, challengeRoot, certFile, keyFile = origRoot, origChallenge, origCert, origKey
		jobMu.Lock()
		lastJob, jobActive = nil, false
		jobMu.Unlock()
	})
	return dir
}

type entry struct {
	name string
	body string
	mode int64
	typ  byte
}

func tarball(t *testing.T, entries []entry) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		header := &tar.Header{Name: e.name, Mode: e.mode, Size: int64(len(e.body)), Typeflag: typ}
		if typ == tar.TypeSymlink {
			header.Linkname = e.body
			header.Size = 0
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	path := filepath.Join(t.TempDir(), "acme.tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const testTag = "3.1.6"

const fakeScript = "#!/bin/sh\nVER=" + testTag + "\necho fake\n"

func TestExtractKeepsOnlyTheScriptAndTheHooks(t *testing.T) {
	prefix := "acme.sh-" + testTag + "/"
	path := tarball(t, []entry{
		{name: prefix, typ: tar.TypeDir, mode: 0o755},
		{name: prefix + "README.md", body: "docs", mode: 0o644},
		{name: prefix + "acme.sh", body: fakeScript, mode: 0o755},
		{name: prefix + "dnsapi/dns_cf.sh", body: "cf", mode: 0o644},
		{name: prefix + "dnsapi/README.md", body: "no", mode: 0o644},
		{name: prefix + "deploy/haproxy.sh", body: "no", mode: 0o755},
		{name: prefix + "notify/mail.sh", body: "no", mode: 0o755},
		{name: "other/acme.sh", body: "no", mode: 0o755},
		{name: prefix + "dnsapi/../../evil.sh", body: "no", mode: 0o755},
	})
	dest := filepath.Join(t.TempDir(), "bin")
	if err := extract(path, dest, testTag); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"README.md", "dnsapi/README.md", "deploy", "notify", "../evil.sh"} {
		if _, err := os.Stat(filepath.Join(dest, rel)); err == nil {
			t.Errorf("%s was extracted", rel)
		}
	}
	info, err := os.Stat(filepath.Join(dest, "acme.sh"))
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("acme.sh: %v %v", err, info)
	}
	if info, err := os.Stat(filepath.Join(dest, "dnsapi", "dns_cf.sh")); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("dns_cf.sh: %v %v", err, info)
	}
}

func TestExtractRefusesLinksOversizeAndEmptyArchives(t *testing.T) {
	prefix := "acme.sh-" + testTag + "/"
	link := tarball(t, []entry{
		{name: prefix + "acme.sh", body: "/etc/passwd", typ: tar.TypeSymlink},
		{name: prefix + "dnsapi/dns_cf.sh", body: "cf", mode: 0o644},
	})
	if err := extract(link, filepath.Join(t.TempDir(), "bin"), testTag); err == nil || !strings.Contains(err.Error(), "regular") {
		t.Fatalf("symlink accepted: %v", err)
	}
	empty := tarball(t, []entry{{name: prefix + "README.md", body: "x", mode: 0o644}})
	if err := extract(empty, filepath.Join(t.TempDir(), "bin"), testTag); err == nil {
		t.Fatal("archive without the script accepted")
	}
	big := tarball(t, []entry{
		{name: prefix + "acme.sh", body: strings.Repeat("x", maxFile+1), mode: 0o755},
		{name: prefix + "dnsapi/dns_cf.sh", body: "cf", mode: 0o644},
	})
	if err := extract(big, filepath.Join(t.TempDir(), "bin"), testTag); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Fatalf("oversize accepted: %v", err)
	}
}

func TestNewerVersion(t *testing.T) {
	cases := []struct {
		installed, candidate string
		want                 bool
	}{
		{"3.1.6", "3.1.7", true},
		{"3.1.6", "3.2", true},
		{"3.1.6", "4.0.0", true},
		{"3.1.6", "3.1.6", false},
		{"3.1.6", "3.1.5", false},
		{"3.1.10", "3.1.9", false},
		{"", "3.1.6", true},
		{"3.1.6", "", false},
		{"3.1.6", "3.1.7-rc1", false},
	}
	for _, tc := range cases {
		if got := newerVersion(tc.installed, tc.candidate); got != tc.want {
			t.Errorf("newerVersion(%q, %q) = %v", tc.installed, tc.candidate, got)
		}
	}
}

func TestParseProviderInfo(t *testing.T) {
	cases := []struct {
		hook, name, site string
		options, alt     int
		first            string
	}{
		{"dns_cf", "CloudFlare", "https://CloudFlare.com", 2, 3, "CF_Key"},
		{"dns_ovh", "OVH.com", "https://OVH.com", 4, 0, "OVH_END_POINT"},
		{"dns_duckdns", "DuckDNS.org", "https://www.DuckDNS.org", 1, 0, "DuckDNS_Token"},
	}
	for _, tc := range cases {
		data, err := os.ReadFile(filepath.Join("testdata", tc.hook+".sh"))
		if err != nil {
			t.Fatal(err)
		}
		p := parseProviderInfo(tc.hook, data)
		if p.Name != tc.name || p.Site != tc.site || len(p.Options) != tc.options || len(p.OptionsAlt) != tc.alt {
			t.Errorf("%s: %+v", tc.hook, p)
			continue
		}
		if p.Options[0].Name != tc.first || p.Options[0].Description == "" {
			t.Errorf("%s first option: %+v", tc.hook, p.Options[0])
		}
		if !strings.HasPrefix(p.Docs, "https://github.com/acmesh-official/acme.sh/wiki/") {
			t.Errorf("%s docs: %q", tc.hook, p.Docs)
		}
	}
	bare := parseProviderInfo("dns_plain", []byte("#!/bin/sh\ndns_plain_add() { :; }\n"))
	if bare.Name != "dns_plain" || len(bare.Options) != 0 || bare.Options == nil {
		t.Fatalf("hook without info: %+v", bare)
	}
}

func TestListProvidersReadsTheInstalledHooks(t *testing.T) {
	useTempTree(t)
	if err := os.MkdirAll(hooksDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, hook := range []string{"dns_cf", "dns_ovh"} {
		data, _ := os.ReadFile(filepath.Join("testdata", hook+".sh"))
		if err := os.WriteFile(filepath.Join(hooksDir(), hook+".sh"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(hooksDir(), "README.md"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(hooksDir(), "dns_Bad-Name.sh"), []byte("x"), 0o644)
	providers, err := ListProviders()
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 2 || providers[0].Hook != "dns_cf" || providers[1].Hook != "dns_ovh" {
		t.Fatalf("providers = %+v", providers)
	}
	if _, ok := provider("dns_ovh"); !ok {
		t.Fatal("dns_ovh not found")
	}
	if _, ok := provider("../etc/passwd"); ok {
		t.Fatal("path accepted as a hook")
	}
}

func TestSettingsNormalize(t *testing.T) {
	good := Settings{Domains: []string{" KVM.Example.org ", "www.kvm.example.org"}, Email: "me@example.org", CA: "letsencrypt", Challenge: "http"}
	if err := good.normalize(); err != nil {
		t.Fatal(err)
	}
	if good.Domains[0] != "kvm.example.org" || good.primary() != "kvm.example.org" {
		t.Fatalf("domains = %v", good.Domains)
	}
	bad := []Settings{
		{Domains: nil, CA: "letsencrypt", Challenge: "http"},
		{Domains: []string{"kvm"}, CA: "letsencrypt", Challenge: "http"},
		{Domains: []string{"10.0.0.2"}, CA: "letsencrypt", Challenge: "http"},
		{Domains: []string{"a.example", "a.example"}, CA: "letsencrypt", Challenge: "http"},
		{Domains: []string{"*.example.org"}, CA: "letsencrypt", Challenge: "http"},
		{Domains: []string{"a.example"}, CA: "letsencrypt", Challenge: "dns"},
		{Domains: []string{"a.example"}, CA: "letsencrypt", Challenge: "dns", DNSProvider: "dns_cf", DNSSleep: 5000},
		{Domains: []string{"a.example"}, CA: "letsencrypt", Challenge: "tls"},
		{Domains: []string{"a.example"}, CA: "unknown", Challenge: "http"},
		{Domains: []string{"a.example"}, CA: "zerossl", Challenge: "http"},
		{Domains: []string{"a.example"}, CA: "letsencrypt", Challenge: "http", Email: "not-an-address"},
		{Domains: []string{"a.example; rm -rf /"}, CA: "letsencrypt", Challenge: "http"},
	}
	for i, s := range bad {
		if err := s.normalize(); err == nil {
			t.Errorf("case %d accepted: %+v", i, s)
		}
	}
	wildcard := Settings{Domains: []string{"*.example.org", "example.org"}, CA: "zerossl", Email: "a@b.cd", Challenge: "dns", DNSProvider: "dns_cf"}
	if err := wildcard.normalize(); err != nil {
		t.Fatal(err)
	}
}

func TestIssueArgs(t *testing.T) {
	useTempTree(t)
	_ = os.MkdirAll(hooksDir(), 0o755)
	data, _ := os.ReadFile(filepath.Join("testdata", "dns_cf.sh"))
	_ = os.WriteFile(filepath.Join(hooksDir(), "dns_cf.sh"), data, 0o644)

	http := Settings{Domains: []string{"kvm.example.org"}, Email: "me@example.org", CA: "letsencrypt_test", Challenge: "http"}
	args, env, secrets, err := issueArgs(http, IssueReq{})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"--issue", "--server letsencrypt_test", "--keylength ec-256", "--force", "-d kvm.example.org", "--accountemail me@example.org", "--webroot " + challengeRoot} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %q", want, joined)
		}
	}
	if len(env) != 0 || len(secrets) != 0 {
		t.Fatalf("env for http: %v %v", env, secrets)
	}

	dns := Settings{Domains: []string{"*.example.org"}, CA: "letsencrypt", Challenge: "dns", DNSProvider: "dns_cf", DNSSleep: 30}
	args, env, secrets, err = issueArgs(dns, IssueReq{DNSEnv: map[string]string{"CF_Token": "tok-123", "CF_Zone_ID": ""}})
	if err != nil {
		t.Fatal(err)
	}
	joined = strings.Join(args, " ")
	if !strings.Contains(joined, "--dns dns_cf") || !strings.Contains(joined, "--dnssleep 30") || strings.Contains(joined, "tok-123") {
		t.Fatalf("args = %q", joined)
	}
	if len(env) != 1 || env[0] != "CF_Token=tok-123" || len(secrets) != 1 || secrets[0] != "tok-123" {
		t.Fatalf("env = %v secrets = %v", env, secrets)
	}

	for name, req := range map[string]IssueReq{
		"foreign variable": {DNSEnv: map[string]string{"LD_PRELOAD": "/x.so"}},
		"no credentials":   {DNSEnv: map[string]string{}},
		"newline":          {DNSEnv: map[string]string{"CF_Token": "a\nb"}},
	} {
		if _, _, _, err := issueArgs(dns, req); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	missing := Settings{Domains: []string{"a.example.org"}, CA: "letsencrypt", Challenge: "dns", DNSProvider: "dns_none"}
	if _, _, _, err := issueArgs(missing, IssueReq{}); err == nil {
		t.Fatal("uninstalled hook accepted")
	}

	google := Settings{Domains: []string{"a.example.org"}, CA: "google", Challenge: "http"}
	if _, _, _, err := issueArgs(google, IssueReq{}); err == nil {
		t.Fatal("Google without EAB accepted")
	}
	args, _, secrets, err = issueArgs(google, IssueReq{EABKid: "kid", EABHmacKey: "hmac-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if joined = strings.Join(args, " "); !strings.Contains(joined, "--server google --") || !strings.Contains(joined, "--eab-kid kid --eab-hmac-key hmac-secret") {
		t.Fatalf("args = %q", joined)
	}
	if len(secrets) != 1 || secrets[0] != "hmac-secret" {
		t.Fatalf("secrets = %v", secrets)
	}
}

// fakeAcme installs a shell script in place of acme.sh that records its
// arguments and environment, writes a pair when asked to install one, and
// exits with the code in $FAKE_EXIT (read from a file so the job's explicit
// environment cannot carry it).
func fakeAcme(t *testing.T, exitCode string) (argsFile, envFile string) {
	t.Helper()
	if err := os.MkdirAll(binDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	argsFile = filepath.Join(root, "args.txt")
	envFile = filepath.Join(root, "env.txt")
	exitFile := filepath.Join(root, "exit.txt")
	if err := os.WriteFile(exitFile, []byte(exitCode), 0o644); err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM := testPair(t)
	script := "#!/bin/sh\nVER=" + testTag + "\n" +
		"printf '%s\\n' \"$@\" >> '" + argsFile + "'\n" +
		"printf -- '----\\n' >> '" + argsFile + "'\n" +
		"env | sort > '" + envFile + "'\n" +
		"echo \"running with token=$CF_Token\"\n" +
		"ps -o comm= -p $$ > '" + filepath.Join(root, "comm.txt") + "' 2>/dev/null || true\n" +
		"stat -c %a '" + dataDir() + "' > '" + filepath.Join(root, "datamode.txt") + "'\n" +
		"stat -c %a \"$(dirname '" + certFile + "')\" > '" + filepath.Join(root, "certmode.txt") + "'\n" +
		"full=''; key=''\n" +
		"while [ $# -gt 0 ]; do case \"$1\" in --fullchain-file) full=$2; shift;; --key-file) key=$2; shift;; esac; shift; done\n" +
		"if [ -n \"$full\" ]; then mkdir -p \"$(dirname \"$full\")\"; printf '%s' '" + string(certPEM) + "' > \"$full\"; printf '%s' '" + string(keyPEM) + "' > \"$key\"; fi\n" +
		"exit $(cat '" + exitFile + "')\n"
	if err := os.WriteFile(filepath.Join(binDir(), "acme.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return argsFile, envFile
}

func testPair(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 64))
	template := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "kvm.example.org"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(90 * 24 * time.Hour), DNSNames: []string{"kvm.example.org"}}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}

// stubConfig replaces the TLS configuration hooks with an in-memory copy.
func stubConfig(t *testing.T, proto string) (*config.Config, *utils.CertificateStore) {
	t.Helper()
	dir := t.TempDir()
	origDefaultCert, origDefaultKey := defaultCertFile, defaultKeyFile
	defaultCertFile, defaultKeyFile = filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key")
	if err := utils.GenerateCertificateFiles(defaultCertFile, defaultKeyFile); err != nil {
		t.Fatal(err)
	}
	conf := &config.Config{Proto: proto, Port: config.Port{Http: 80, Https: 443}, Cert: config.Cert{Crt: defaultCertFile, Key: defaultKeyFile}}
	store := &utils.CertificateStore{}
	origRead, origUpdate, origStore := readConfig, updateTLS, serverCertificate
	// The real configuration is a file replaced atomically; the in-memory
	// stand-in needs a lock for the handler and the job reading and writing
	// it at the same time.
	var mu sync.Mutex
	readConfig = func() (*config.Config, error) {
		mu.Lock()
		defer mu.Unlock()
		c := *conf
		return &c, nil
	}
	updateTLS = func(p string, cert config.Cert) error {
		mu.Lock()
		defer mu.Unlock()
		conf.Proto, conf.Cert = p, cert
		return nil
	}
	serverCertificate = func() *utils.CertificateStore { return store }
	t.Cleanup(func() {
		readConfig, updateTLS, serverCertificate = origRead, origUpdate, origStore
		defaultCertFile, defaultKeyFile = origDefaultCert, origDefaultKey
	})
	return conf, store
}

func waitJob(t *testing.T) *Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !running() {
			return lastRun()
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return nil
}

func newRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	s := NewService()
	r := gin.New()
	r.GET("/status", s.GetStatus)
	r.POST("/issue", s.Issue)
	r.POST("/renew", s.Renew)
	r.POST("/remove", s.Remove)
	r.POST("/uninstall", s.Uninstall)
	return r
}

type response struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data Status `json:"data"`
}

func call(t *testing.T, r *gin.Engine, method, path string, body any) response {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var rsp response
	if err := json.Unmarshal(rec.Body.Bytes(), &rsp); err != nil {
		t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body.String())
	}
	return rsp
}

func TestIssueRunsAcmeInstallsThePairAndServesIt(t *testing.T) {
	useTempTree(t)
	argsFile, envFile := fakeAcme(t, "0")
	_ = os.MkdirAll(hooksDir(), 0o755)
	data, _ := os.ReadFile(filepath.Join("testdata", "dns_cf.sh"))
	_ = os.WriteFile(filepath.Join(hooksDir(), "dns_cf.sh"), data, 0o644)
	conf, store := stubConfig(t, "https")
	r := newRouter()

	req := IssueReq{
		Settings: Settings{Domains: []string{"kvm.example.org"}, Email: "me@example.org", CA: "letsencrypt", Challenge: "dns", DNSProvider: "dns_cf"},
		DNSEnv:   map[string]string{"CF_Token": "tok-secret-123"},
	}
	rsp := call(t, r, http.MethodPost, "/issue", req)
	if rsp.Code != 0 || !rsp.Data.Running {
		t.Fatalf("issue: %+v", rsp)
	}
	// A second job is refused while the first runs or, once it is done,
	// accepted: either way nothing breaks; here we only wait.
	job := waitJob(t)
	if job == nil || !job.OK || job.Action != "issue" {
		t.Fatalf("job = %+v", job)
	}
	if strings.Contains(job.Output, "tok-secret-123") || !strings.Contains(job.Output, "token=***") {
		t.Fatalf("output not scrubbed: %q", job.Output)
	}
	args, _ := os.ReadFile(argsFile)
	text := string(args)
	if !strings.Contains(text, "--issue") || !strings.Contains(text, "--install-cert") || !strings.Contains(text, "--home\n"+binDir()) || !strings.Contains(text, "--config-home\n"+dataDir()) {
		t.Fatalf("acme.sh arguments:\n%s", text)
	}
	env, _ := os.ReadFile(envFile)
	if !strings.Contains(string(env), "CF_Token=tok-secret-123") || !strings.Contains(string(env), "LE_CONFIG_HOME="+dataDir()) || strings.Contains(string(env), "GOPATH") {
		t.Fatalf("environment:\n%s", env)
	}
	for _, name := range []string{"datamode.txt", "certmode.txt"} {
		if mode, _ := os.ReadFile(filepath.Join(root, name)); strings.TrimSpace(string(mode)) != "700" {
			t.Fatalf("%s = %q", name, mode)
		}
	}
	cmd := command(context.Background(), nil, "--version")
	if cmd.Path != script() || len(cmd.Args) != 2 || cmd.Args[1] != "--version" {
		t.Fatalf("acme.sh not run directly: %q %q", cmd.Path, cmd.Args)
	}
	if conf.Cert.Crt != certFile || conf.Cert.Key != keyFile || conf.Proto != "https" {
		t.Fatalf("configuration = %+v", conf)
	}
	served, err := store.GetCertificate(nil)
	if err != nil || served.Leaf.Subject.CommonName != "kvm.example.org" {
		t.Fatalf("served = %v %v", served, err)
	}
	settings, err := loadSettings()
	if err != nil || settings.IssuedAt == "" || settings.DNSProvider != "dns_cf" {
		t.Fatalf("settings = %+v %v", settings, err)
	}
	saved, _ := os.ReadFile(settingsFile())
	if strings.Contains(string(saved), "tok-secret") {
		t.Fatal("the settings file holds the credential")
	}
	status := call(t, r, http.MethodGet, "/status", nil).Data
	if !status.Installed || status.Version != testTag || !status.InUse || status.Certificate == nil || status.LastRun == nil || !status.LastRun.OK {
		t.Fatalf("status = %+v", status)
	}
}

func TestIssueWithHTTPNeedsPort80(t *testing.T) {
	useTempTree(t)
	fakeAcme(t, "0")
	conf, _ := stubConfig(t, "https")
	conf.Port.Http = 8080
	r := newRouter()
	req := IssueReq{Settings: Settings{Domains: []string{"kvm.example.org"}, CA: "letsencrypt", Challenge: "http"}}
	rsp := call(t, r, http.MethodPost, "/issue", req)
	if rsp.Code != -1 || !strings.Contains(rsp.Msg, "port to be 80 (it is 8080)") {
		t.Fatalf("issue on port 8080: %+v", rsp)
	}
	if running() {
		t.Fatal("a job started")
	}
}

func TestScrubLeavesShortSecretsAlone(t *testing.T) {
	out := scrub("token=abcdef key=x all done", []string{"abcdef", "x", ""})
	if out != "token=*** key=x all done" {
		t.Fatalf("scrub = %q", out)
	}
}

// TestExtractTheRealRelease runs the allowlist over a real acme.sh source
// archive when ACME_TARBALL names one (not on CI).
func TestExtractTheRealRelease(t *testing.T) {
	path := os.Getenv("ACME_TARBALL")
	if path == "" {
		t.Skip("ACME_TARBALL not set")
	}
	dest := filepath.Join(t.TempDir(), "bin")
	if err := extract(path, dest, "3.1.6"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dest, "acme.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if scriptVersion(data) != "3.1.6" {
		t.Fatalf("version = %q", scriptVersion(data))
	}
	hooks, _ := filepath.Glob(filepath.Join(dest, "dnsapi", "dns_*.sh"))
	if len(hooks) != 198 {
		t.Fatalf("%d hooks extracted", len(hooks))
	}
	for _, rel := range []string{"deploy", "notify", "README.md", "dnsapi/README.md", "Dockerfile"} {
		if _, err := os.Stat(filepath.Join(dest, rel)); err == nil {
			t.Errorf("%s extracted", rel)
		}
	}
	providers, err := listProviders(filepath.Join(dest, "dnsapi"))
	if err != nil {
		t.Fatal(err)
	}
	named := 0
	for _, p := range providers {
		if p.Name != p.Hook {
			named++
		}
	}
	t.Logf("%d hooks, %d with an info block", len(providers), named)
	if named < 150 {
		t.Fatalf("only %d of %d hooks have an info block", named, len(providers))
	}
}

func TestIssueFailureKeepsTheConfiguration(t *testing.T) {
	useTempTree(t)
	fakeAcme(t, "1")
	conf, store := stubConfig(t, "https")
	r := newRouter()
	req := IssueReq{Settings: Settings{Domains: []string{"kvm.example.org"}, CA: "letsencrypt", Challenge: "http"}}
	if rsp := call(t, r, http.MethodPost, "/issue", req); rsp.Code != 0 {
		t.Fatalf("issue: %+v", rsp)
	}
	job := waitJob(t)
	if job.OK || !strings.Contains(job.Error, "exited with code 1") {
		t.Fatalf("job = %+v", job)
	}
	if conf.Cert.Crt != defaultCertFile {
		t.Fatalf("configuration changed: %+v", conf)
	}
	if _, err := store.GetCertificate(nil); err == nil {
		t.Fatal("a pair was loaded after a failed issue")
	}
	if settings, err := loadSettings(); !os.IsNotExist(err) {
		t.Fatalf("settings saved by a failed issue: %+v %v", settings, err)
	}
}

func TestRenewNotDueAndRemove(t *testing.T) {
	useTempTree(t)
	argsFile, _ := fakeAcme(t, "2")
	conf, store := stubConfig(t, "https")
	settings := &Settings{Domains: []string{"kvm.example.org"}, CA: "letsencrypt", Challenge: "http", IssuedAt: "2026-10-12T00:00:00Z"}
	if err := saveSettings(settings); err != nil {
		t.Fatal(err)
	}
	conf.Cert = config.Cert{Crt: certFile, Key: keyFile}
	r := newRouter()

	// --cron with nothing due is a success without a renewal.
	renewDue()
	job := waitJob(t)
	if !job.OK || !job.NotDue || job.Action != "cron" {
		t.Fatalf("cron job = %+v", job)
	}
	// Renew now forces; exit 2 is then a failure.
	if rsp := call(t, r, http.MethodPost, "/renew", nil); rsp.Code != 0 {
		t.Fatalf("renew: %+v", rsp)
	}
	job = waitJob(t)
	if job.OK || job.Action != "renew" {
		t.Fatalf("renew job = %+v", job)
	}
	args, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(args), "--renew\n-d\nkvm.example.org\n--ecc\n--force") {
		t.Fatalf("renew arguments:\n%s", args)
	}

	// Remove: back to the generated pair, files and settings gone.
	_ = os.MkdirAll(certDir(), 0o700)
	_ = os.WriteFile(certFile, []byte("x"), 0o644)
	_ = os.WriteFile(keyFile, []byte("y"), 0o600)
	rsp := call(t, r, http.MethodPost, "/remove", nil)
	if rsp.Code != 0 || rsp.Data.InUse || rsp.Data.Settings != nil || rsp.Data.Certificate != nil {
		t.Fatalf("remove: %+v", rsp)
	}
	if conf.Cert.Crt != defaultCertFile {
		t.Fatalf("configuration after remove: %+v", conf.Cert)
	}
	if served, err := store.GetCertificate(nil); err != nil || served.Leaf.Subject.CommonName != "localhost" {
		t.Fatalf("served after remove: %v %v", served, err)
	}
	if _, err := os.Stat(keyFile); !os.IsNotExist(err) {
		t.Fatal("key kept")
	}
	args, _ = os.ReadFile(argsFile)
	if !strings.Contains(string(args), "--remove\n-d\nkvm.example.org\n--ecc") {
		t.Fatalf("remove arguments:\n%s", args)
	}
	// Uninstall removes the tree.
	if rsp := call(t, r, http.MethodPost, "/uninstall", nil); rsp.Code != 0 || rsp.Data.Installed {
		t.Fatalf("uninstall: %+v", rsp)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("acme directory kept")
	}
}

func TestReloadIfInUseServesARenewedPair(t *testing.T) {
	useTempTree(t)
	conf, store := stubConfig(t, "https")
	conf.Cert = config.Cert{Crt: certFile, Key: keyFile}
	_ = os.MkdirAll(certDir(), 0o700)
	certPEM, keyPEM := testPair(t)
	_ = os.WriteFile(certFile, certPEM, 0o644)
	_ = os.WriteFile(keyFile, keyPEM, 0o600)
	reloadIfInUse()
	first, err := store.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM = testPair(t)
	_ = os.WriteFile(certFile, certPEM, 0o644)
	_ = os.WriteFile(keyFile, keyPEM, 0o600)
	reloadIfInUse()
	second, _ := store.GetCertificate(nil)
	if bytes.Equal(first.Certificate[0], second.Certificate[0]) {
		t.Fatal("renewed pair not served")
	}
	// Not in use: nothing happens.
	conf.Cert = config.Cert{Crt: defaultCertFile, Key: defaultKeyFile}
	certPEM, keyPEM = testPair(t)
	_ = os.WriteFile(certFile, certPEM, 0o644)
	_ = os.WriteFile(keyFile, keyPEM, 0o600)
	reloadIfInUse()
	if third, _ := store.GetCertificate(nil); !bytes.Equal(second.Certificate[0], third.Certificate[0]) {
		t.Fatal("reloaded while not in use")
	}
}

func TestNextRenewal(t *testing.T) {
	useTempTree(t)
	dir := filepath.Join(dataDir(), "kvm.example.org_ecc")
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(filepath.Join(dir, "kvm.example.org.conf"), []byte("Le_Domain='kvm.example.org'\nLe_NextRenewTime='1800000000'\nLe_NextRenewTimeStr='x'\n"), 0o600)
	if got := nextRenewal("kvm.example.org"); got != "2027-01-15T08:00:00Z" {
		t.Fatalf("nextRenewal = %q", got)
	}
	if got := nextRenewal("other.example.org"); got != "" {
		t.Fatalf("missing conf: %q", got)
	}
}

func TestChallengeHandler(t *testing.T) {
	useTempTree(t)
	EnsureChallengeDir()
	dir := filepath.Join(challengeRoot, ".well-known", "acme-challenge")
	if err := os.WriteFile(filepath.Join(dir, "tok_1-A"), []byte("tok_1-A.thumb"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(challengeRoot, "secret"), []byte("no"), 0o644)
	h := ChallengeHandler()
	get := func(path string) (int, string) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code, rec.Body.String()
	}
	if code, body := get(ChallengePrefix + "tok_1-A"); code != 200 || body != "tok_1-A.thumb" {
		t.Fatalf("token: %d %q", code, body)
	}
	for _, path := range []string{ChallengePrefix + "missing", ChallengePrefix + "../secret", ChallengePrefix + "..%2Fsecret", ChallengePrefix, "/secret", ChallengePrefix + "a/b"} {
		if code, _ := get(path); code != 404 {
			t.Errorf("%s: %d", path, code)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, ChallengePrefix+"tok_1-A", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", rec.Code)
	}
}

func TestStartJobIsSingleFlight(t *testing.T) {
	useTempTree(t)
	fakeAcme(t, "0")
	_ = os.WriteFile(filepath.Join(binDir(), "acme.sh"), []byte("#!/bin/sh\nsleep 1\n"), 0o755)
	if err := startJob("issue", nil, nil, []step{{args: []string{"--issue"}}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := startJob("renew", nil, nil, []step{{args: []string{"--renew"}}}, nil); err == nil {
		t.Fatal("second job accepted while the first runs")
	}
	if job := waitJob(t); !job.OK {
		t.Fatalf("job = %+v", job)
	}
	if err := startJob("renew", nil, nil, []step{{args: []string{"--renew"}}}, nil); err != nil {
		t.Fatal(err)
	}
	waitJob(t)
}

func TestInstallTakesTheLatestStableReleaseAndChecksItsVersion(t *testing.T) {
	useTempTree(t)
	archive := func(tag, ver string) []byte {
		prefix := "acme.sh-" + tag + "/"
		path := tarball(t, []entry{
			{name: prefix + "acme.sh", body: "#!/bin/sh\nVER=" + ver + "\n", mode: 0o755},
			{name: prefix + "dnsapi/dns_cf.sh", body: "cf", mode: 0o644},
			{name: prefix + "deploy/x.sh", body: "no", mode: 0o755},
		})
		data, _ := os.ReadFile(path)
		return data
	}
	latest := "3.1.7"
	archives := map[string][]byte{"3.1.7": archive("3.1.7", "3.1.7"), "3.1.8": archive("3.1.8", "3.1.6")}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/latest" {
			if r.Header.Get("User-Agent") == "" {
				http.Error(w, "no user agent", 400)
				return
			}
			_, _ = io.WriteString(w, `{"tag_name":"`+latest+`","draft":false,"prerelease":false}`)
			return
		}
		tag := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/tags/"), ".tar.gz")
		w.Header().Set("Content-Type", "application/x-gzip")
		_, _ = w.Write(archives[tag])
	}))
	defer server.Close()
	origLatest, origBase := latestURL, tarballBase
	latestURL, tarballBase = server.URL+"/latest", server.URL+"/tags/"
	defer func() { latestURL, tarballBase = origLatest, origBase }()
	latestMu.Lock()
	latestKnown = nil
	latestMu.Unlock()

	// Data survives an install: a file from before.
	_ = os.MkdirAll(dataDir(), 0o700)
	_ = os.WriteFile(filepath.Join(dataDir(), "account.conf"), []byte("kept"), 0o600)

	release, err := Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if release.Tag != "3.1.7" || installedVersion() != "3.1.7" {
		t.Fatalf("release %+v, installed %q", release, installedVersion())
	}
	if _, err := os.Stat(filepath.Join(binDir(), "deploy")); err == nil {
		t.Fatal("deploy hooks extracted")
	}
	if kept, _ := os.ReadFile(filepath.Join(dataDir(), "account.conf")); string(kept) != "kept" {
		t.Fatal("data directory touched by the install")
	}
	st := status()
	if st.Latest == nil || st.Latest.Tag != "3.1.7" || st.UpdateAvailable {
		t.Fatalf("status = %+v", st)
	}

	// A newer release is offered as an update; one whose script disagrees
	// with the tag is refused and the installed version stays.
	latest = "3.1.8"
	if _, err := latestRelease(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := status(); !st.UpdateAvailable {
		t.Fatalf("update not offered: %+v", st)
	}
	if _, err := Install(context.Background()); err == nil || !strings.Contains(err.Error(), "says version") {
		t.Fatalf("mismatching script accepted: %v", err)
	}
	if installedVersion() != "3.1.7" {
		t.Fatalf("installed version after the refused update: %q", installedVersion())
	}
	if _, err := os.Stat(filepath.Join(root, "bin.staging")); !os.IsNotExist(err) {
		t.Fatal("staging directory kept")
	}

	// Pre-releases are not stable.
	latest = "3.2.0-beta"
	if _, err := latestRelease(context.Background()); err == nil {
		t.Fatal("pre-release tag accepted")
	}
}

// issued sets up a certificate already issued for primary and in use.
func issued(t *testing.T, conf *config.Config, primary string) {
	t.Helper()
	if err := saveSettings(&Settings{Domains: []string{primary}, CA: "letsencrypt", Challenge: "http", IssuedAt: "2026-10-01T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(dataDir(), primary+"_ecc"), 0o700)
	_ = os.WriteFile(filepath.Join(dataDir(), primary+"_ecc", primary+".conf"), []byte("Le_Domain='"+primary+"'\n"), 0o600)
	conf.Cert = config.Cert{Crt: certFile, Key: keyFile}
}

func lastInvocation(t *testing.T, argsFile string) string {
	t.Helper()
	data, _ := os.ReadFile(argsFile)
	blocks := strings.Split(strings.TrimSuffix(string(data), "----\n"), "----\n")
	return blocks[len(blocks)-1]
}

func TestFailedReissueKeepsTheCertificateInUseAndItsSettings(t *testing.T) {
	useTempTree(t)
	argsFile, _ := fakeAcme(t, "1")
	conf, _ := stubConfig(t, "https")
	issued(t, conf, "old.example.org")
	r := newRouter()

	req := IssueReq{Settings: Settings{Domains: []string{"new.example.org"}, CA: "letsencrypt", Challenge: "http"}}
	if rsp := call(t, r, http.MethodPost, "/issue", req); rsp.Code != 0 {
		t.Fatalf("issue: %+v", rsp)
	}
	if job := waitJob(t); job.OK {
		t.Fatalf("job = %+v", job)
	}
	settings, err := loadSettings()
	if err != nil || settings.primary() != "old.example.org" {
		t.Fatalf("settings after a failed reissue: %+v %v", settings, err)
	}
	if conf.Cert.Crt != certFile {
		t.Fatalf("configuration changed: %+v", conf.Cert)
	}
	args, _ := os.ReadFile(argsFile)
	if strings.Contains(string(args), "--remove\n-d\nold.example.org") {
		t.Fatal("the record in use was retired by a failed reissue")
	}
	if !strings.Contains(string(args), "--remove\n-d\nnew.example.org\n--ecc") {
		t.Fatalf("the failed new record was not retired:\n%s", args)
	}
	// Renew and Remove still act on the certificate in use.
	if rsp := call(t, r, http.MethodPost, "/renew", nil); rsp.Code != 0 {
		t.Fatalf("renew: %+v", rsp)
	}
	waitJob(t)
	if last := lastInvocation(t, argsFile); !strings.Contains(last, "--renew\n-d\nold.example.org\n") {
		t.Fatalf("renew targeted:\n%s", last)
	}
}

func TestReplacingThePrimaryRetiresTheOldRecordAndRenewsOnlyTheNewOne(t *testing.T) {
	useTempTree(t)
	argsFile, _ := fakeAcme(t, "0")
	conf, _ := stubConfig(t, "https")
	issued(t, conf, "old.example.org")
	r := newRouter()

	req := IssueReq{Settings: Settings{Domains: []string{"new.example.org"}, CA: "letsencrypt", Challenge: "http"}}
	if rsp := call(t, r, http.MethodPost, "/issue", req); rsp.Code != 0 {
		t.Fatalf("issue: %+v", rsp)
	}
	if job := waitJob(t); !job.OK {
		t.Fatalf("job = %+v", job)
	}
	args, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(args), "--remove\n-d\nold.example.org\n--ecc") {
		t.Fatalf("the replaced record was not retired:\n%s", args)
	}
	if _, err := os.Stat(filepath.Join(dataDir(), "old.example.org_ecc")); !os.IsNotExist(err) {
		t.Fatalf("the replaced record's directory kept: %v", err)
	}
	if settings, _ := loadSettings(); settings.primary() != "new.example.org" {
		t.Fatalf("settings = %+v", settings)
	}

	// The scheduled renewal acts on the selected certificate only, without
	// --force (acme.sh then skips it until it is due) and never --cron.
	renewDue()
	waitJob(t)
	last := lastInvocation(t, argsFile)
	if !strings.Contains(last, "--renew\n-d\nnew.example.org\n--ecc\n") || strings.Contains(last, "--force") {
		t.Fatalf("scheduled renewal:\n%s", last)
	}
	if data, _ := os.ReadFile(argsFile); strings.Contains(string(data), "--cron") {
		t.Fatal("--cron used")
	}
}

func TestActivationFailureFailsTheIssueJob(t *testing.T) {
	useTempTree(t)
	fakeAcme(t, "0")
	stubConfig(t, "https")
	updateTLS = func(string, config.Cert) error { return errors.New("read-only file system") }
	r := newRouter()
	req := IssueReq{Settings: Settings{Domains: []string{"kvm.example.org"}, CA: "letsencrypt", Challenge: "http"}}
	if rsp := call(t, r, http.MethodPost, "/issue", req); rsp.Code != 0 {
		t.Fatalf("issue: %+v", rsp)
	}
	job := waitJob(t)
	if job.OK || !strings.Contains(job.Error, "could not be put in use") || !strings.Contains(job.Error, "read-only file system") {
		t.Fatalf("job = %+v", job)
	}
}

func TestRenewalReloadFailureFailsTheJob(t *testing.T) {
	useTempTree(t)
	fakeAcme(t, "0")
	conf, _ := stubConfig(t, "https")
	issued(t, conf, "kvm.example.org")
	_ = os.MkdirAll(certDir(), 0o700)
	_ = os.WriteFile(certFile, []byte("garbage"), 0o644)
	_ = os.WriteFile(keyFile, []byte("garbage"), 0o600)
	r := newRouter()
	if rsp := call(t, r, http.MethodPost, "/renew", nil); rsp.Code != 0 {
		t.Fatalf("renew: %+v", rsp)
	}
	job := waitJob(t)
	if job.OK || !strings.Contains(job.Error, "renewed certificate could not be put in use") {
		t.Fatalf("job = %+v", job)
	}
}

func TestScheduledRenewalWaitsForAdministrativeOperations(t *testing.T) {
	useTempTree(t)
	argsFile, _ := fakeAcme(t, "0")
	conf, _ := stubConfig(t, "https")
	issued(t, conf, "kvm.example.org")
	opMu.Lock()
	renewDue()
	running := running()
	opMu.Unlock()
	if running || lastRun() != nil {
		t.Fatal("the scheduled renewal started during an administrative operation")
	}
	if _, err := os.Stat(argsFile); !os.IsNotExist(err) {
		t.Fatal("acme.sh ran")
	}
	renewDue()
	if job := waitJob(t); job == nil || job.Action != "cron" {
		t.Fatalf("the next round did not run: %+v", job)
	}
}
