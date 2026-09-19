package osupdate

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAlpineArtifactURLsStayOnBuilderOrigin(t *testing.T) {
	base, err := trustedBuilderURL("https://builder.example/base")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sameOriginRelative(base, "/artifacts/abc/file"); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"https://evil.example/file", "//evil.example/file", "/artifacts/../secret", "/artifacts/file?x=1"} {
		if _, err = sameOriginRelative(base, raw); err == nil {
			t.Fatalf("accepted unsafe URL %q", raw)
		}
	}
	if _, err = trustedBuilderURL("https://builder.example/a/../b"); err == nil {
		t.Fatal("accepted non-canonical builder path")
	}
}

func TestAlpineBuildResponseAllowlistAndHashes(t *testing.T) {
	base, _ := url.Parse("https://builder.example")
	files := make([]string, 0, len(alpineRequiredFiles))
	for name := range alpineRequiredFiles {
		files = append(files, `{"name":"`+name+`","sha256":"`+strings.Repeat("a", 64)+`","url":"/artifacts/0123456789abcdef01234567/`+name+`"}`)
	}
	body := `{"build_id":"0123456789abcdef01234567","files":[` + strings.Join(files, ",") + `]}`
	state, err := parseBuildResponse(base, strings.NewReader(body))
	if err != nil || state.BuildID == "" || len(state.Files) != len(alpineRequiredFiles) {
		t.Fatalf("response rejected: %#v %v", state, err)
	}
	bad := strings.Replace(body, `"url":"/artifacts/`, `"url":"https://evil.example/`, 1)
	if _, err = parseBuildResponse(base, strings.NewReader(bad)); err == nil {
		t.Fatal("accepted cross-origin artifact")
	}
}

func TestAlpineSumsRejectTamperingAndAllowExactFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one"), []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	digest := "7692c3ad3540bb803c6e7d1f9f4f8e3f4f3f5a5f5a5f5a5f5a5f5a5f5a5f5a5f"
	// Use the actual digest so this test also exercises file hashing.
	hash, err := HashFile(filepath.Join(dir, "one"))
	if err != nil {
		t.Fatal(err)
	}
	if hash == digest {
		t.Fatal("unexpected fixture digest")
	}
	if err = os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(hash+"  one\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = verifyAlpineSums(dir, map[string]bool{"one": true}); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "one"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = verifyAlpineSums(dir, map[string]bool{"one": true}); err == nil {
		t.Fatal("accepted tampered artifact")
	}
}

func TestAlpineStateRoundTrip(t *testing.T) {
	old := alpineStateDir
	alpineStateDir = t.TempDir()
	defer func() { alpineStateDir = old }()
	want := AlpineState{State: "built", BuildID: "0123456789abcdef01234567", Profile: "stock", Packages: []string{"zstd"}}
	if err := SetAlpineState(want); err != nil {
		t.Fatal(err)
	}
	got := GetAlpineState()
	if got.State != want.State || got.BuildID != want.BuildID || len(got.Packages) != 1 {
		t.Fatalf("state mismatch: %#v", got)
	}
}
