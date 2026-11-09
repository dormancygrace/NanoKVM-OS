package acme

import (
	"archive/tar"
	"bytes"
	"compress/gzip"

	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"testing"
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

// fakeAcme installs a shell script in place of acme.sh that records its
// arguments and environment, writes a pair when asked to install one, and
// exits with the code in $FAKE_EXIT (read from a file so the job's explicit
// environment cannot carry it).

// stubConfig replaces the TLS configuration hooks with an in-memory copy.

// The real configuration is a file replaced atomically; the in-memory
// stand-in needs a lock for the handler and the job reading and writing
// it at the same time.

// A second job is refused while the first runs or, once it is done,
// accepted: either way nothing breaks; here we only wait.

func TestScrubLeavesShortSecretsAlone(t *testing.T) {
	out := scrub("token=abcdef key=x all done", []string{"abcdef", "x", ""})
	if out != "token=*** key=x all done" {
		t.Fatalf("scrub = %q", out)
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

// Data survives an install: a file from before.

// A newer release is offered as an update; one whose script disagrees
// with the tag is refused and the installed version stays.

// Pre-releases are not stable.

// issued sets up a certificate already issued for primary and in use.

// Renew and Remove still act on the certificate in use.
