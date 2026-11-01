package osupdate

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestChannelSwitchPreservesRepositoriesAndClearsOffers(t *testing.T) {
	dir := communityFixture(t, "# keep this\n"+communityRepositories+"@custom https://example.test/repo\n")
	old := apkStateDir
	apkStateDir = filepath.Join(dir, "state")
	t.Cleanup(func() { apkStateDir = old })
	if err := os.MkdirAll(apkStateDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := saveNotice(UpdateNotice{State: "ok", CheckedAt: 42, Updates: []SoftwareUpdate{{Name: "nanokvm", Available: "9.0"}}}); err != nil {
		t.Fatal(err)
	}
	if err := saveAPKStatus(APKStatus{State: "ready", Upgrades: "old beta"}); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(repositoriesPath)
	if err := SetChannel("beta"); err != nil {
		t.Fatal(err)
	}
	if got := GetChannel(); got.Channel != "beta" || !got.Available {
		t.Fatal(got)
	}
	got, _ := os.ReadFile(repositoriesPath)
	want := strings.Replace(string(original), "NanoKVM-OS/alpine/", "NanoKVM-OS/beta/alpine/", 1)
	if string(got) != want {
		t.Fatalf("repositories changed: %s", got)
	}
	if n := GetNotice(); n.CheckedAt != 0 || len(n.Updates) != 0 {
		t.Fatal(n)
	}
	if s := GetAPKStatus(); s.State != "idle" || s.Upgrades != "" {
		t.Fatal(s)
	}
	if err := SetChannel("stable"); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(repositoriesPath)
	if string(got) != string(original) {
		t.Fatalf("roundtrip changed unrelated lines: %s", got)
	}
}

func TestChannelRejectsCustomAndAmbiguousRepositories(t *testing.T) {
	for _, data := range []string{"https://example.test/nanokvm/repo\n", communityOff + communityOff,
		communityOff + "https://example.test/nanokvm/repo\n", "@testing " + communityOff} {
		t.Run(data, func(t *testing.T) {
			communityFixture(t, data)
			old := apkStateDir
			apkStateDir = t.TempDir()
			t.Cleanup(func() { apkStateDir = old })
			if got := GetChannel(); got.Available || got.Channel != "custom" {
				t.Fatal("custom repository accepted")
			}
			if SetChannel("beta") == nil {
				t.Fatal("changed custom repository")
			}
			got, _ := os.ReadFile(repositoriesPath)
			if string(got) != data {
				t.Fatal("modified")
			}
		})
	}
}

func TestStableRollbackScopeAndPrereleaseDetection(t *testing.T) {
	dir := communityFixture(t, communityOff)
	oldDB, oldWorld := installedDBPath, worldPath
	installedDBPath, worldPath = filepath.Join(dir, "installed"), filepath.Join(dir, "world")
	t.Cleanup(func() { installedDBPath, worldPath = oldDB, oldWorld })
	os.WriteFile(installedDBPath, []byte("P:nanokvm-rustdesk\nV:0.6.0_beta1-r0\n\n"), 0600)
	if GetChannel().RollbackAvailable {
		t.Fatal("add-on exposed firmware rollback")
	}
	os.WriteFile(installedDBPath, []byte("P:nanokvm\nV:3.4.0_beta1-r0\n\n"), 0600)
	if !GetChannel().RollbackAvailable {
		t.Fatal("missing rollback")
	}
	os.WriteFile(repositoriesPath, []byte(strings.Replace(communityOff, "OS/alpine", "OS/beta/alpine", 1)), 0600)
	if GetChannel().RollbackAvailable {
		t.Fatal("rollback while on beta")
	}
	var output []string
	for _, name := range corePackages {
		output = append(output, name+"-3.3.4-r0")
	}
	good := strings.Join(output, "\n")
	if _, err := stablePackageTargets(good); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Join(output[1:], "\n"), strings.Replace(good, "3.3.4", "3.4.0_beta1", 1)} {
		if _, err := stablePackageTargets(bad); err == nil {
			t.Fatal("unsafe rollback set accepted")
		}
	}
}

func TestStableRollbackBeforeTPUPackage(t *testing.T) {
	var output []string
	for _, name := range corePackages {
		if name != "nanokvm-tpu" {
			output = append(output, name+"-3.6.0-r0")
		}
	}
	targets, err := stablePackageTargets(strings.Join(output, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if joined := " " + strings.Join(targets, " ") + " "; !strings.Contains(joined, " !nanokvm-tpu ") {
		t.Fatalf("a release without nanokvm-tpu must remove it: %v", targets)
	}
	db := "P:nanokvm-tpu\nV:3.7.0-r0\n\nP:nanokvm-model-ocr\nV:1.0.0-r0\nD:nanokvm-os>=3.7.0 nanokvm-tpu-abi=1\n\n" +
		"P:nanokvm-model-ocr-latin\nV:1.0.0-r0\nD:nanokvm-tpu-abi=1 nanokvm-model-ocr\n\nP:nanokvm-ipmi\nV:0.1.0-r2\nD:openrc\n\n"
	if got := tpuDependents(targets, db); !slices.Equal(got, []string{"nanokvm-model-ocr", "nanokvm-model-ocr-latin"}) {
		t.Fatalf("model add-ons must block the rollback: %v", got)
	}
	var current []string
	for _, name := range corePackages {
		current = append(current, name+"-3.7.0-r0")
	}
	if targets, err = stablePackageTargets(strings.Join(current, "\n")); err != nil || tpuDependents(targets, db) != nil {
		t.Fatalf("a release with nanokvm-tpu keeps the model add-ons: %v %v", targets, err)
	}
}

func TestStableRollbackBeforeSG2002Rename(t *testing.T) {
	renamed := []string{"nanokvm-kernel", "nanokvm-kmod", "nanokvm-firmware", "nanokvm-bootloader"}
	var output []string
	for _, name := range corePackages {
		if !slices.Contains(renamed, name) {
			output = append(output, name+"-3.6.0-r0")
		}
	}
	targets, err := stablePackageTargets(strings.Join(output, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range renamed {
		if !slices.Contains(targets, "!"+name) || !slices.Contains(targets, name+"-sg2002=3.6.0-r0") {
			t.Fatalf("%s must go back to %s-sg2002: %v", name, name, targets)
		}
	}
	// Neither name: refused, the kernel is never just removed.
	var without []string
	for _, line := range output {
		if !strings.HasPrefix(line, "nanokvm-kernel-sg2002-") {
			without = append(without, line)
		}
	}
	if _, err = stablePackageTargets(strings.Join(without, "\n")); err == nil {
		t.Fatal("a release without any kernel package accepted")
	}
}

func TestStableRollbackBeforeCustomOpenSSL(t *testing.T) {
	var output []string
	for _, name := range corePackages {
		if name != "nanokvm-openssl" {
			output = append(output, name+"-3.5.0-r0")
		}
	}
	targets, err := stablePackageTargets(strings.Join(output, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	joined := " " + strings.Join(targets, " ") + " "
	if !strings.Contains(joined, " !nanokvm-openssl ") || !strings.Contains(joined, " openssl ") {
		t.Fatalf("legacy release must atomically restore Alpine OpenSSL: %v", targets)
	}
	if _, err = stablePackageTargets(strings.Join(output[1:], "\n")); err == nil {
		t.Fatal("another missing core component must still reject rollback")
	}
}
