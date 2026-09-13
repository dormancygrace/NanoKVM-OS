// nkos-addons is the small, statically linked APK/runtime boundary for
// NanoKVM OS.  It intentionally uses only the Go standard library: no target
// Python, shell package hooks or host libc are required.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	exUsage     = 2
	exAPK       = 3
	exLifecycle = 4
	exPolicy    = 5

	defaultRoot     = "/opt/nkos"
	defaultState    = "/data/.nkos-apk"
	defaultRun      = "/run/nkos-addon"
	defaultAPK      = "/usr/bin/apk"
	defaultScratch  = "/kvmapp/.os-update/addon-resolver"
	contractPath    = "/usr/share/nkos/addons-contract.json"
	contractVersion = 1
)

var (
	idRE = regexp.MustCompile(`^[a-z0-9][a-z0-9+_.-]*$`)
	// APK versions are parsed and ordered by apk-tools.  Keep this grammar
	// broad enough for native versions such as 2.3-r1 and 1.4.10_rc1-r0;
	// the native parser remains authoritative for ordering and package data.
	apkVersionRE = regexp.MustCompile(`^[0-9][0-9A-Za-z._-]{0,95}$`)
	// Image ABI versions are an independent three-component contract.  An OS
	// release string must never silently change the ABI provider.
	abiVersionRE   = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	addonIntentRE  = regexp.MustCompile(`^(nkos-addon-[a-z0-9][a-z0-9+_.-]*)(?:[<>=~]{1,2}[0-9A-Za-z][0-9A-Za-z._-]{0,95})?$`)
	baseABI        = regexp.MustCompile(`^nkos-base-abi=[0-9]+\.[0-9]+\.[0-9]+$`)
	serverAPI      = regexp.MustCompile(`^nkos-server-api=[0-9]+$`)
	featureRE      = regexp.MustCompile(`^nkos-feature-[a-z0-9][a-z0-9-]*=[0-9]+$`)
	providerRE     = regexp.MustCompile(`^(?:nkos-base-abi|nkos-server-api|nkos-feature-[a-z0-9][a-z0-9-]*)(?:=[0-9A-Za-z][0-9A-Za-z._-]{0,95})?$`)
	providerNameRE = regexp.MustCompile(`^(?:nkos-base-abi|nkos-server-api|nkos-feature-[a-z0-9][a-z0-9-]*)$`)
	digestRE       = regexp.MustCompile(`^[a-f0-9]{64}$`)
	commandRE      = regexp.MustCompile(`^/opt/nkos/addons/[a-z0-9][a-z0-9+_.-]*/[^;&|<>` + "`" + `$()\\']+$`)
	packageName    = regexp.MustCompile(`^nkos-addon-[a-z0-9][a-z0-9+_.-]*$`)
	packagePath    = regexp.MustCompile(`^packages/[a-z0-9][a-z0-9+_.-]*\.apk$`)
	repositoryPath = regexp.MustCompile(`^repository/riscv64/[a-z0-9][a-z0-9+_.-]*\.apk$`)
)

type keySpec struct {
	ID        string `json:"id"`
	Filename  string `json:"filename"`
	PublicPEM string `json:"public_key_pem"`
}

type trustSpec struct {
	Keys []keySpec `json:"keys"`
}

type repositorySpec struct {
	URL    string   `json:"url"`
	KeyIDs []string `json:"key_ids"`
}

// targetContract is immutable image capability and trust metadata. Addon
// selection and repository snapshots deliberately live only in checkpoints.
type targetContract struct {
	Format       int              `json:"format"`
	BaseABI      string           `json:"base_abi"`
	ServerAPI    int              `json:"server_api"`
	Features     []string         `json:"features"`
	Trust        trustSpec        `json:"trust"`
	Repositories []repositorySpec `json:"repositories"`
}

type serviceSpec struct {
	Name           string `json:"name"`
	Command        string `json:"command"`
	Stop           string `json:"stop,omitempty"`
	DefaultEnabled bool   `json:"default_enabled"`
	Restart        string `json:"restart"`
}

type addonSpec struct {
	Schema        int           `json:"schema,omitempty"`
	ID            string        `json:"id"`
	Package       string        `json:"package"`
	Version       string        `json:"version"`
	SourceVersion string        `json:"source_version,omitempty"`
	Pkgver        string        `json:"pkgver,omitempty"`
	Pkgrel        *int          `json:"pkgrel,omitempty"`
	Arch          string        `json:"arch,omitempty"`
	BaseABI       string        `json:"base_abi"`
	ServerAPI     string        `json:"server_api"`
	Features      []string      `json:"features"`
	Config        string        `json:"config"`
	Data          string        `json:"data"`
	Services      []serviceSpec `json:"services"`
	Preserve      []string      `json:"preserve"`
}

type checkpointPackage struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
}

type restoreCheckpoint struct {
	Format         int                 `json:"format"`
	ContractSHA256 string              `json:"contract_sha256"`
	World          []string            `json:"world"`
	Packages       []checkpointPackage `json:"packages"`
}

type installedPackage struct {
	Name          string   `json:"name"`
	Version       string   `json:"version"`
	Description   string   `json:"description"`
	Arch          string   `json:"arch"`
	InstalledSize int64    `json:"installed-size"`
	Contents      []string `json:"contents"`
}

type packageDump struct {
	Info struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Arch    string `json:"arch"`
	} `json:"info"`
	Scripts  map[string]string `json:"scripts"`
	Triggers []string          `json:"triggers"`
	Paths    []packageDumpPath `json:"paths"`
}

type packageACL struct {
	Mode uint32 `json:"mode"`
}

type packageDumpPath struct {
	Name  string            `json:"name"`
	ACL   packageACL        `json:"acl"`
	Files []packageDumpFile `json:"files"`
}

type packageDumpFile struct {
	Name   string     `json:"name"`
	Target string     `json:"target"`
	ACL    packageACL `json:"acl"`
}

// apkValidationContext supplies the target trust, cache, and immutable
// provider paths used while inspecting a package. Package signatures are
// checked before payload installation, and the native installer verifies again.
type apkValidationContext struct {
	repoFile  string
	keysDir   string
	cacheDir  string
	providers []string
}

type packageSource struct {
	path string
	pkg  installedPackage
}

type manager struct {
	root    string
	state   string
	run     string
	apk     string
	scratch string
}

func newManager() *manager {
	root, state, run, apk, scratch := defaultRoot, defaultState, defaultRun, defaultAPK, defaultScratch
	if v := os.Getenv("NKOS_APK_ROOT"); v != "" {
		root = v
	}
	if v := os.Getenv("NKOS_APK_STATE"); v != "" {
		state = v
	}
	if v := os.Getenv("NKOS_APK_RUN"); v != "" {
		run = v
	}
	if v := os.Getenv("NKOS_APK_BIN"); v != "" {
		apk = v
	}
	if v := os.Getenv("NKOS_APK_SCRATCH"); v != "" {
		scratch = v
	}
	return &manager{root: root, state: state, run: run, apk: apk, scratch: scratch}
}

func die(code int, format string, args ...any) {
	fmt.Fprintf(os.Stderr, "nkos-addons: "+format+"\n", args...)
	os.Exit(code)
}

func (m *manager) requireRoot() {
	if os.Geteuid() != 0 {
		die(exUsage, "must run as root")
	}
	if info, err := os.Lstat(m.root); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		die(exPolicy, "APK root must be a real directory: %s", m.root)
	}
	for _, p := range []string{m.state, filepath.Join(m.state, "cache"), m.run, filepath.Join(m.root, "addons"), filepath.Join(m.root, "etc/apk")} {
		if err := os.MkdirAll(p, 0700); err != nil {
			die(exAPK, "create state path %s: %v", p, err)
		}
		if info, err := os.Lstat(p); err != nil || info.Mode()&os.ModeSymlink != 0 {
			die(exPolicy, "state path is a symlink: %s", p)
		}
	}
}

func (m *manager) lock(inherited bool) func() {
	if inherited {
		f := os.NewFile(uintptr(3), "update-lock")
		if f == nil {
			die(exLifecycle, "canonical update lock fd3 is required")
		}
		info, err := f.Stat()
		lockPath := "/kvmapp/.os-update/lock"
		if value := os.Getenv("NKOS_UPDATE_LOCK"); value != "" {
			lockPath = value
		}
		canonical, canonicalErr := os.Lstat(lockPath)
		if err != nil || canonicalErr != nil || !info.Mode().IsRegular() || !canonical.Mode().IsRegular() || !os.SameFile(info, canonical) {
			die(exLifecycle, "invalid inherited update lock")
		}
		if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			die(exLifecycle, "canonical update lock is not held")
		}
		syscall.CloseOnExec(int(f.Fd()))
		return func() {}
	}
	path := filepath.Join(m.state, "lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_CLOEXEC, 0600)
	if err != nil {
		die(exLifecycle, "open addon lock: %v", err)
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		die(exLifecycle, "another addon transaction is in progress")
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }
}

func regular(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("not a regular file")
	}
	return info, nil
}

func readJSON(path string, value any) error {
	if _, err := regular(path); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}

func writeJSON(path string, value any, mode os.FileMode) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".nkos-write-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err = tmp.Chmod(mode); err == nil {
		_, err = tmp.Write(append(data, '\n'))
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func validID(id string) bool { return idRE.MatchString(id) }

func normalizeAddonName(value string) (string, error) {
	value = strings.TrimSpace(strings.SplitN(value, "#", 2)[0])
	match := addonIntentRE.FindStringSubmatch(value)
	if len(match) == 0 {
		return "", fmt.Errorf("unsupported addon package expression %q", value)
	}
	return match[1], nil
}

func (m *manager) loadContract(path string) (targetContract, error) {
	var value targetContract
	if err := readJSON(path, &value); err != nil {
		return value, fmt.Errorf("read contract: %w", err)
	}
	if value.Format != contractVersion || !abiVersionRE.MatchString(value.BaseABI) || value.ServerAPI < 1 {
		return value, errors.New("invalid target base ABI or server API")
	}
	if len(value.Features) == 0 {
		return value, errors.New("target feature set is empty")
	}
	seen := map[string]bool{}
	for _, feature := range value.Features {
		if !featureRE.MatchString(feature) || seen[feature] {
			return value, fmt.Errorf("invalid target feature %q", feature)
		}
		seen[feature] = true
	}
	if len(value.Trust.Keys) == 0 || len(value.Repositories) == 0 {
		return value, errors.New("target trust or repository set is empty")
	}
	keyIDs := map[string]bool{}
	for _, key := range value.Trust.Keys {
		if !validID(key.ID) || !idRE.MatchString(key.Filename) ||
			!strings.HasPrefix(key.PublicPEM, "-----BEGIN PUBLIC KEY-----\n") || !strings.HasSuffix(key.PublicPEM, "-----END PUBLIC KEY-----\n") || keyIDs[key.ID] {
			return value, errors.New("invalid target trust key")
		}
		keyIDs[key.ID] = true
	}
	for _, repository := range value.Repositories {
		if !strings.HasPrefix(repository.URL, "https://") || strings.ContainsAny(repository.URL, "\x00\r\n") || len(repository.KeyIDs) == 0 {
			return value, errors.New("invalid target repository")
		}
		for _, id := range repository.KeyIDs {
			if !keyIDs[id] {
				return value, errors.New("target repository references an unknown trust key")
			}
		}
	}
	return value, nil
}

func imageContractPath() string {
	if path := os.Getenv("NKOS_ADDON_CONTRACT"); path != "" {
		return path
	}
	return contractPath
}

func (m *manager) imageContract() (targetContract, string, error) {
	path := imageContractPath()
	value, err := m.loadContract(path)
	if err != nil {
		return value, "", err
	}
	hash, _, err := hashFile(path)
	return value, hash, err
}

func contractCompatible(image, target targetContract) error {
	if target.BaseABI != image.BaseABI || target.ServerAPI != image.ServerAPI {
		return fmt.Errorf("target ABI/API %s/%d does not match image %s/%d", target.BaseABI, target.ServerAPI, image.BaseABI, image.ServerAPI)
	}
	imageFeatures := map[string]bool{}
	for _, f := range image.Features {
		imageFeatures[f] = true
	}
	for _, f := range target.Features {
		if !imageFeatures[f] {
			return fmt.Errorf("target requires unavailable immutable feature %s", f)
		}
	}
	return nil
}

func materializeTrust(contract targetContract, dir string) (string, string, error) {
	keys := filepath.Join(dir, "keys")
	if err := os.RemoveAll(dir); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(keys, 0700); err != nil {
		return "", "", err
	}
	if err := os.Mkdir(filepath.Join(dir, "cache"), 0700); err != nil {
		return "", "", err
	}
	for _, key := range contract.Trust.Keys {
		if err := os.WriteFile(filepath.Join(keys, key.Filename), []byte(key.PublicPEM), 0600); err != nil {
			return "", "", err
		}
	}
	var repositories strings.Builder
	for _, repository := range contract.Repositories {
		repositories.WriteString("v3 ")
		repositories.WriteString(repository.URL)
		repositories.WriteByte('\n')
	}
	repo := filepath.Join(dir, "repositories")
	if err := os.WriteFile(repo, []byte(repositories.String()), 0600); err != nil {
		return "", "", err
	}
	return repo, keys, nil
}

func (m *manager) apkCommand(repoFile, keysDir, cacheDir string, args ...string) *exec.Cmd {
	global := []string{"--root", m.root, "--repositories-file", repoFile, "--keys-dir", keysDir, "--cache-dir", cacheDir}
	return exec.Command(m.apk, append(global, args...)...)
}

func (m *manager) runAPK(repoFile, keysDir, cacheDir string, args ...string) error {
	if _, err := regular(m.apk); err != nil {
		return fmt.Errorf("apk executable %s: %w", m.apk, err)
	}
	cmd := m.apkCommand(repoFile, keysDir, cacheDir, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("apk %s: %s", strings.Join(args, " "), strings.TrimSpace(string(output)))
	}
	return nil
}

func (m *manager) installedPackages(repoFile, keysDir, cacheDir string) ([]installedPackage, error) {
	cmd := m.apkCommand(repoFile, keysDir, cacheDir, "query", "--format", "json", "--installed", "--fields", "name,version,description,arch,installed-size,contents", "*")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("query resolved addon closure: %w", err)
	}
	var packages []installedPackage
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&packages); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("invalid APK query result")
	}
	return packages, nil
}

func (m *manager) inspectPackageMetadata(path string) (packageDump, error) {
	output, err := exec.Command(m.apk, "adbdump", "--format", "json", path).Output()
	if err != nil {
		return packageDump{}, fmt.Errorf("inspect addon package metadata: %w", err)
	}
	var dump packageDump
	if err = json.Unmarshal(output, &dump); err != nil {
		return packageDump{}, errors.New("invalid addon package metadata")
	}
	return dump, nil
}

func (m *manager) defaultAPKValidationContext() apkValidationContext {
	return apkValidationContext{
		repoFile: filepath.Join(m.root, "etc/apk/repositories"),
		keysDir:  filepath.Join(m.root, "etc/apk/keys"),
		cacheDir: filepath.Join(m.state, "cache"),
	}
}

func packageHasExecutable(dump packageDump) bool {
	for _, directory := range dump.Paths {
		for _, file := range directory.Files {
			if file.ACL.Mode&0111 != 0 {
				return true
			}
		}
	}
	return false
}

// validateStaticELF validates only executable-mode ELF payloads. Executable
// scripts and other non-ELF files remain valid; the static capability applies
// to native ELF binaries only.
func validateStaticELF(path string) error {
	f, err := elf.Open(path)
	if err != nil {
		file, openErr := os.Open(path)
		if openErr != nil {
			return fmt.Errorf("open executable %s: %w", filepath.Base(path), openErr)
		}
		var magic [4]byte
		n, _ := io.ReadFull(file, magic[:])
		_ = file.Close()
		if n < len(magic) || !bytes.Equal(magic[:], []byte{0x7f, 'E', 'L', 'F'}) {
			return nil
		}
		return fmt.Errorf("malformed ELF executable %s: %w", filepath.Base(path), err)
	}
	defer f.Close()

	if f.Class != elf.ELFCLASS64 {
		return fmt.Errorf("executable %s is not ELF64", filepath.Base(path))
	}
	if f.Data != elf.ELFDATA2LSB {
		return fmt.Errorf("executable %s is not little-endian", filepath.Base(path))
	}
	if f.Machine != elf.EM_RISCV {
		return fmt.Errorf("executable %s is not RISC-V", filepath.Base(path))
	}
	if f.Type != elf.ET_EXEC && f.Type != elf.ET_DYN {
		return fmt.Errorf("executable %s has unsupported ELF type %s", filepath.Base(path), f.Type)
	}
	for _, program := range f.Progs {
		if program.Type == elf.PT_INTERP {
			return fmt.Errorf("executable %s has an external ELF interpreter", filepath.Base(path))
		}
	}
	libraries, err := f.ImportedLibraries()
	if err != nil {
		return fmt.Errorf("inspect ELF dependencies for %s: %w", filepath.Base(path), err)
	}
	if len(libraries) != 0 {
		return fmt.Errorf("executable %s has dynamic ELF dependencies: %s", filepath.Base(path), strings.Join(libraries, ", "))
	}
	for _, program := range f.Progs {
		if program.Type != elf.PT_DYNAMIC {
			continue
		}
		const dynamicEntrySize = uint64(16)
		if program.Filesz%dynamicEntrySize != 0 {
			return fmt.Errorf("inspect ELF dynamic segment for %s: invalid entry size", filepath.Base(path))
		}
		reader := program.Open()
		for remaining := program.Filesz; remaining > 0; remaining -= dynamicEntrySize {
			var entry [16]byte
			if _, err := io.ReadFull(reader, entry[:]); err != nil {
				return fmt.Errorf("inspect ELF dynamic segment for %s: %w", filepath.Base(path), err)
			}
			tag := elf.DynTag(f.ByteOrder.Uint64(entry[:8]))
			if tag == elf.DT_NEEDED {
				return fmt.Errorf("executable %s has dynamic ELF dependencies", filepath.Base(path))
			}
			if tag == elf.DT_NULL {
				break
			}
		}
	}
	return nil
}

func (m *manager) initializeValidationProviders(context apkValidationContext, sandbox *manager) error {
	if len(context.providers) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(context.providers))
	for index, provider := range context.providers {
		parts := strings.SplitN(provider, "=", 2)
		if len(parts) != 2 || !providerRE.MatchString(provider) || !providerNameRE.MatchString(parts[0]) || seen[provider] {
			return fmt.Errorf("invalid immutable APK validation provider %q", provider)
		}
		seen[provider] = true
		args := []string{"add", "--no-network", "--no-scripts", "--no-commit-hooks"}
		if index == 0 {
			args = append(args, "--initdb")
		}
		args = append(args, "--virtual", provider)
		if err := sandbox.runAPK(context.repoFile, context.keysDir, context.cacheDir, args...); err != nil {
			return fmt.Errorf("initialize immutable APK validation provider %s: %w", provider, err)
		}
	}
	return nil
}

func (m *manager) validateExecutablePayload(path string, expectedName string, dump packageDump, context apkValidationContext) error {
	if !packageHasExecutable(dump) {
		return nil
	}
	if _, err := regular(m.apk); err != nil {
		return fmt.Errorf("APK executable %s: %w", m.apk, err)
	}
	destination, err := os.MkdirTemp("", "nkos-addon-payload-")
	if err != nil {
		return fmt.Errorf("create ELF validation directory: %w", err)
	}
	defer os.RemoveAll(destination)

	// The standalone apk extract app requires explicit directory dirents in
	// the archive. apk installation is the supported path for normal APKs
	// whose parent directories are implicit. The target root is disposable,
	// and all package scripts/hooks stay disabled.
	if err = os.MkdirAll(filepath.Join(destination, "etc/apk"), 0700); err != nil {
		return fmt.Errorf("create ELF validation APK state: %w", err)
	}
	if err = os.WriteFile(filepath.Join(destination, "etc/apk/arch"), []byte("riscv64\n"), 0600); err != nil {
		return fmt.Errorf("initialize ELF validation architecture: %w", err)
	}
	sandbox := *m
	sandbox.root = destination
	if err = m.initializeValidationProviders(context, &sandbox); err != nil {
		return err
	}
	args := []string{"add", "--no-network", "--no-scripts", "--no-commit-hooks"}
	if len(context.providers) == 0 {
		args = append(args, "--initdb")
	}
	args = append(args, path)
	if err = sandbox.runAPK(context.repoFile, context.keysDir, context.cacheDir, args...); err != nil {
		return fmt.Errorf("install addon payload for ELF validation: %w", err)
	}

	id := strings.TrimPrefix(expectedName, "nkos-addon-")
	payloadRoot := filepath.Join(destination, "addons", id)
	info, err := os.Lstat(payloadRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		if err == nil {
			err = errors.New("payload root is not a real directory")
		}
		return fmt.Errorf("installed addon payload root is invalid: %w", err)
	}
	privateLibraries := false
	var descriptor addonSpec
	if data, err := os.ReadFile(filepath.Join(payloadRoot, "addon.json")); err == nil {
		if err := json.Unmarshal(data, &descriptor); err != nil {
			return err
		}
		for _, feature := range descriptor.Features {
			if feature == privateLibrariesFeature {
				privateLibraries = true
			}
		}
	}
	if privateLibraries {
		available := false
		for _, provider := range context.providers {
			if provider == privateLibrariesFeature {
				available = true
			}
		}
		if !available {
			return errors.New("image lacks private library capability")
		}
	}
	return filepath.WalkDir(payloadRoot, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current == payloadRoot {
			return nil
		}
		rel, err := filepath.Rel(payloadRoot, current)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return errors.New("installed addon payload escapes validation root")
		}
		owned := filepath.ToSlash(filepath.Join("addons", id, rel))
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("installed addon payload contains a symlink: %s", owned)
		}
		if entry.IsDir() {
			return nil
		}
		fileInfo, err := entry.Info()
		if err != nil {
			return err
		}
		if !fileInfo.Mode().IsRegular() {
			return fmt.Errorf("installed addon payload contains a special file: %s", owned)
		}
		if privateLibraries {
			if rel == "lib/libc.so" || strings.HasPrefix(filepath.Base(rel), "ld-musl-") {
				return errors.New("addon must use image musl, not replace it")
			}
			return validatePrivateELF(current, payloadRoot, id)
		}
		if fileInfo.Mode()&0111 == 0 {
			return nil
		}
		if err := validateStaticELF(current); err != nil {
			return err
		}
		return nil
	})
}

func (m *manager) validatePackageMetadata(path string, expected installedPackage) error {
	return m.validatePackageMetadataContext(path, expected, m.defaultAPKValidationContext())
}

func (m *manager) validatePackageMetadataContext(path string, expected installedPackage, context apkValidationContext) error {
	dump, err := m.inspectPackageMetadata(path)
	if err != nil {
		return err
	}
	if dump.Info.Name != expected.Name || dump.Info.Version != expected.Version || dump.Info.Arch != "riscv64" || len(dump.Scripts) != 0 || len(dump.Triggers) != 0 {
		return fmt.Errorf("addon package %s has mismatched metadata, scripts, or triggers", expected.Name)
	}
	id := strings.TrimPrefix(expected.Name, "nkos-addon-")
	prefix := "addons/" + id
	for _, directory := range dump.Paths {
		name := strings.TrimPrefix(directory.Name, "/")
		if name != "" && name != "addons" && name != prefix && !strings.HasPrefix(name, prefix+"/") {
			return fmt.Errorf("addon package %s owns path outside %s", expected.Name, prefix)
		}
		for _, file := range directory.Files {
			owned := filepath.ToSlash(filepath.Join(name, file.Name))
			if !strings.HasPrefix(owned, prefix+"/") || strings.Contains(file.Target, "..") || strings.HasPrefix(file.Target, "/") {
				return fmt.Errorf("addon package %s has an unsafe file or link", expected.Name)
			}
		}
	}
	if err := m.validateExecutablePayload(path, expected.Name, dump, context); err != nil {
		return fmt.Errorf("addon package %s has an invalid native payload: %w", expected.Name, err)
	}
	return nil
}

func (m *manager) readWorld() ([]string, error) {
	path := filepath.Join(m.root, "etc/apk/world")
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	seen := map[string]bool{}
	var world []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		intent := strings.TrimSpace(strings.SplitN(scanner.Text(), "#", 2)[0])
		if intent == "" {
			continue
		}
		if providerRE.MatchString(intent) {
			continue
		}
		root, parseErr := normalizeAddonName(intent)
		if parseErr != nil || seen[root] {
			return nil, errors.New("APK world contains a duplicate or unsupported addon expression")
		}
		seen[root] = true
		world = append(world, intent)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	sort.Strings(world)
	return world, nil
}

func safePackagePath(path string) bool {
	return packagePath.MatchString(path) || repositoryPath.MatchString(path)
}

func copyFile(source, destination string, mode os.FileMode) error {
	if _, err := regular(source); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	if copyErr == nil {
		copyErr = out.Sync()
	}
	if closeErr := out.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		_ = os.Remove(destination)
	}
	return copyErr
}

func (m *manager) verifyPackageSignatures(repoFile, keysDir, cacheDir string, paths []string) error {
	for _, path := range paths {
		if err := m.runAPK(repoFile, keysDir, cacheDir, "verify", path); err != nil {
			return fmt.Errorf("APK signature verification failed for %s: %w", filepath.Base(path), err)
		}
	}
	return nil
}

func (m *manager) prepare(contractFile, stage string) error {
	if _, err := regular(contractFile); err != nil {
		return fmt.Errorf("target contract: %w", err)
	}
	if info, err := os.Lstat(stage); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe addon restore stage")
	}
	target, err := m.loadContract(contractFile)
	if err != nil {
		return err
	}
	contractHash, _, err := hashFile(contractFile)
	if err != nil {
		return err
	}
	world, err := m.readWorld()
	if err != nil {
		return err
	}
	packagesDir := filepath.Join(stage, "packages")
	if err = os.Mkdir(packagesDir, 0700); err != nil && !os.IsExist(err) {
		return err
	}
	if entries, readErr := os.ReadDir(packagesDir); readErr != nil || len(entries) != 0 {
		return errors.New("addon restore package directory is not empty")
	}
	checkpoint := restoreCheckpoint{Format: 1, ContractSHA256: contractHash, World: world, Packages: []checkpointPackage{}}
	if len(world) == 0 {
		if err = os.WriteFile(filepath.Join(stage, "packages.sha256"), nil, 0600); err != nil {
			return err
		}
		return writeJSON(filepath.Join(stage, "checkpoint.json"), checkpoint, 0600)
	}
	trustDir := filepath.Join(stage, ".target-trust")
	repoFile, keysDir, err := materializeTrust(target, trustDir)
	if err != nil {
		return err
	}
	defer os.RemoveAll(trustDir)
	resolverRoot := m.scratch
	if resolverRoot == "" || !filepath.IsAbs(resolverRoot) || filepath.Clean(resolverRoot) == "/" {
		return errors.New("unsafe POSIX addon resolver scratch path")
	}
	if err = os.RemoveAll(resolverRoot); err != nil {
		return err
	}
	defer os.RemoveAll(resolverRoot)
	if err = os.MkdirAll(filepath.Join(resolverRoot, "etc/apk"), 0700); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(resolverRoot, "etc/apk/arch"), []byte("riscv64\n"), 0600); err != nil {
		return err
	}
	var scratchFS syscall.Statfs_t
	if err = syscall.Statfs(resolverRoot, &scratchFS); err != nil {
		return err
	}
	if int64(scratchFS.Type) == 0x2011BAB0 || scratchFS.Bavail*uint64(scratchFS.Bsize) < 512<<20 {
		return errors.New("addon resolver requires 512 MiB free on a POSIX filesystem")
	}
	resolver := *m
	resolver.root = resolverRoot
	for index, provider := range resolver.imageProviderWorld(target) {
		providerArgs := []string{"add", "--no-network", "--no-scripts", "--no-commit-hooks"}
		if index == 0 {
			providerArgs = append(providerArgs, "--initdb")
		}
		providerArgs = append(providerArgs, "--virtual", provider)
		if err = resolver.runAPK(repoFile, keysDir, filepath.Join(trustDir, "cache"), providerArgs...); err != nil {
			return fmt.Errorf("cannot initialize target image provider %s: %w", provider, err)
		}
	}
	args := []string{"add", "--no-scripts", "--no-commit-hooks"}
	args = append(args, world...)
	if err = resolver.runAPK(repoFile, keysDir, filepath.Join(trustDir, "cache"), args...); err != nil {
		return fmt.Errorf("target repository cannot install installed addons; remove unsupported addons before updating: %w", err)
	}
	installed, err := resolver.installedPackages(repoFile, keysDir, filepath.Join(trustDir, "cache"))
	if err != nil {
		return err
	}
	expectedProviders := map[string]string{"nkos-base-abi": target.BaseABI, "nkos-server-api": strconv.Itoa(target.ServerAPI)}
	for _, feature := range target.Features {
		parts := strings.SplitN(feature, "=", 2)
		expectedProviders[parts[0]] = parts[1]
	}
	seenProviders, seenRoots := map[string]bool{}, map[string]bool{}
	var closure []installedPackage
	var expanded int64
	for _, pkg := range installed {
		if want, reserved := expectedProviders[pkg.Name]; reserved {
			if pkg.Version != want || pkg.Arch != "noarch" || pkg.Description != "virtual meta package" || len(pkg.Contents) != 0 {
				return fmt.Errorf("repository replaced immutable image provider %s", pkg.Name)
			}
			seenProviders[pkg.Name] = true
			continue
		}
		if providerNameRE.MatchString(pkg.Name) {
			return fmt.Errorf("repository supplied undeclared reserved provider %s", pkg.Name)
		}
		if !packageName.MatchString(pkg.Name) || !apkVersionRE.MatchString(pkg.Version) || pkg.Arch != "riscv64" || pkg.InstalledSize < 0 {
			return errors.New("resolved closure contains an invalid addon package")
		}
		expanded += pkg.InstalledSize
		if expanded > 512<<20 {
			return errors.New("resolved addon closure expands beyond 512 MiB")
		}
		closure = append(closure, pkg)
		seenRoots[pkg.Name] = true
	}
	for name := range expectedProviders {
		if !seenProviders[name] {
			return fmt.Errorf("target resolver lost immutable provider %s", name)
		}
	}
	for _, intent := range world {
		root, _ := normalizeAddonName(intent)
		if !seenRoots[root] {
			return fmt.Errorf("target repository did not install selected addon %s", root)
		}
	}
	sort.Slice(closure, func(i, j int) bool { return closure[i].Name < closure[j].Name })
	paths := make([]string, 0, len(closure))
	var sums strings.Builder
	wantedClosure := map[string]installedPackage{}
	for _, pkg := range closure {
		wantedClosure[pkg.Name+"="+pkg.Version] = pkg
	}
	cacheEntries, err := os.ReadDir(filepath.Join(trustDir, "cache"))
	if err != nil {
		return err
	}
	seenClosure := map[string]bool{}
	sources := make([]packageSource, 0, len(closure))
	for _, entry := range cacheEntries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".apk") {
			continue
		}
		source := filepath.Join(trustDir, "cache", entry.Name())
		dump, inspectErr := resolver.inspectPackageMetadata(source)
		if inspectErr != nil {
			return inspectErr
		}
		if providerNameRE.MatchString(dump.Info.Name) {
			return fmt.Errorf("repository cached forbidden immutable provider %s", dump.Info.Name)
		}
		key := dump.Info.Name + "=" + dump.Info.Version
		pkg, wanted := wantedClosure[key]
		if !wanted || seenClosure[key] {
			return fmt.Errorf("APK cache contains unexpected or duplicate package %s", key)
		}
		sources = append(sources, packageSource{path: source, pkg: pkg})
		paths = append(paths, source)
		seenClosure[key] = true
	}
	for key := range wantedClosure {
		if !seenClosure[key] {
			return fmt.Errorf("native solver cache omitted resolved package %s", key)
		}
	}
	if err = resolver.verifyPackageSignatures(repoFile, keysDir, filepath.Join(trustDir, "cache"), paths); err != nil {
		return err
	}
	for _, source := range sources {
		if err = resolver.validatePackageMetadataContext(source.path, source.pkg, apkValidationContext{repoFile: repoFile, keysDir: keysDir, cacheDir: filepath.Join(trustDir, "cache"), providers: resolver.imageProviderWorld(target)}); err != nil {
			return err
		}
		name := source.pkg.Name + "-" + source.pkg.Version + ".apk"
		path := filepath.Join(packagesDir, name)
		if err = copyFile(source.path, path, 0600); err != nil {
			return err
		}
		got, size, hashErr := hashFile(path)
		if hashErr != nil || size < 1 || size > 64<<20 {
			return fmt.Errorf("invalid cached addon package %s", source.pkg.Name)
		}
		checkpoint.Packages = append(checkpoint.Packages, checkpointPackage{Name: source.pkg.Name, Version: source.pkg.Version, Path: "packages/" + name, Size: size, SHA256: got})
		fmt.Fprintf(&sums, "%s  packages/%s\n", got, name)
	}
	if err = os.WriteFile(filepath.Join(stage, "packages.sha256"), []byte(sums.String()), 0600); err != nil {
		return err
	}
	return writeJSON(filepath.Join(stage, "checkpoint.json"), checkpoint, 0600)
}

func (m *manager) validateCheckpoint(stage, contractFile string, verifySignatures bool) (restoreCheckpoint, targetContract, error) {
	var checkpoint restoreCheckpoint
	if err := readJSON(filepath.Join(stage, "checkpoint.json"), &checkpoint); err != nil {
		return checkpoint, targetContract{}, err
	}
	if checkpoint.Format != 1 || !digestRE.MatchString(checkpoint.ContractSHA256) {
		return checkpoint, targetContract{}, errors.New("invalid addon restore checkpoint")
	}
	contract, err := m.loadContract(contractFile)
	if err != nil {
		return checkpoint, contract, err
	}
	hash, _, err := hashFile(contractFile)
	if err != nil || hash != checkpoint.ContractSHA256 {
		return checkpoint, contract, errors.New("addon restore checkpoint targets a different contract")
	}
	seenWorld := map[string]bool{}
	for _, intent := range checkpoint.World {
		root, parseErr := normalizeAddonName(intent)
		if parseErr != nil || seenWorld[root] {
			return checkpoint, contract, errors.New("invalid checkpoint addon world")
		}
		seenWorld[root] = true
	}
	seenNames, seenPaths := map[string]bool{}, map[string]bool{}
	paths := make([]string, 0, len(checkpoint.Packages))
	if len(checkpoint.Packages) > 1024 {
		return checkpoint, contract, errors.New("addon closure contains too many packages")
	}
	var total int64
	for _, pkg := range checkpoint.Packages {
		if !packageName.MatchString(pkg.Name) || providerNameRE.MatchString(pkg.Name) || !apkVersionRE.MatchString(pkg.Version) || !packagePath.MatchString(pkg.Path) || !digestRE.MatchString(pkg.SHA256) || pkg.Size < 1 || pkg.Size > 64<<20 || seenNames[pkg.Name] || seenPaths[pkg.Path] {
			return checkpoint, contract, errors.New("invalid addon closure entry")
		}
		seenNames[pkg.Name], seenPaths[pkg.Path] = true, true
		total += pkg.Size
		if total > 256<<20 {
			return checkpoint, contract, errors.New("addon closure is too large")
		}
		path := filepath.Join(stage, filepath.FromSlash(pkg.Path))
		info, statErr := regular(path)
		if statErr != nil || info.Size() != pkg.Size {
			return checkpoint, contract, fmt.Errorf("missing or unsafe addon closure package %s", pkg.Name)
		}
		got, _, hashErr := hashFile(path)
		if hashErr != nil || got != pkg.SHA256 {
			return checkpoint, contract, fmt.Errorf("addon closure package checksum mismatch for %s", pkg.Name)
		}
		paths = append(paths, path)
	}
	for _, intent := range checkpoint.World {
		root, _ := normalizeAddonName(intent)
		if !seenNames[root] {
			return checkpoint, contract, fmt.Errorf("checkpoint omitted installed addon %s", root)
		}
	}
	var validationContext apkValidationContext
	var trustDir string
	if len(paths) != 0 {
		trustDir = filepath.Join(stage, ".verify-trust")
		repo, keys, trustErr := materializeTrust(contract, trustDir)
		if trustErr != nil {
			return checkpoint, contract, trustErr
		}
		defer os.RemoveAll(trustDir)
		validationContext = apkValidationContext{repoFile: repo, keysDir: keys, cacheDir: filepath.Join(trustDir, "cache"), providers: m.imageProviderWorld(contract)}
		// Seed the disposable native resolver cache with the complete checkpoint
		// closure so dependency resolution remains offline without touching the
		// caller's stage package files or any live APK cache.
		for _, packagePath := range paths {
			cachePath := filepath.Join(validationContext.cacheDir, filepath.Base(packagePath))
			if err = copyFile(packagePath, cachePath, 0600); err != nil {
				return checkpoint, contract, fmt.Errorf("seed addon validation cache: %w", err)
			}
		}
		if verifySignatures {
			if trustErr = m.verifyPackageSignatures(repo, keys, validationContext.cacheDir, paths); trustErr != nil {
				return checkpoint, contract, trustErr
			}
		}
		for index, pkg := range checkpoint.Packages {
			if err = m.validatePackageMetadataContext(paths[index], installedPackage{Name: pkg.Name, Version: pkg.Version, Arch: "riscv64"}, validationContext); err != nil {
				return checkpoint, contract, err
			}
		}
	}
	return checkpoint, contract, nil
}

func (m *manager) verifyUpgrade(contractFile, stage string) error {
	checkpoint, _, err := m.validateCheckpoint(stage, contractFile, true)
	if err != nil {
		return err
	}
	world, err := m.readWorld()
	if err != nil {
		return err
	}
	if strings.Join(world, "\n") != strings.Join(checkpoint.World, "\n") {
		return errors.New("addon world changed after upgrade preflight")
	}
	return nil
}

func (m *manager) imageProviderWorld(contract targetContract) []string {
	world := []string{"nkos-base-abi=" + contract.BaseABI, "nkos-server-api=" + strconv.Itoa(contract.ServerAPI)}
	world = append(world, contract.Features...)
	sort.Strings(world)
	return world
}

func (m *manager) verifyImageTrust(contract targetContract) error {
	for _, key := range contract.Trust.Keys {
		path := filepath.Join(m.root, "etc/apk/keys", key.Filename)
		actual, err := os.ReadFile(path)
		if err != nil || string(actual) != key.PublicPEM {
			return fmt.Errorf("target trust key %s does not match the signed image contract", key.ID)
		}
	}
	return nil
}

func (m *manager) baseReady() (targetContract, error) {
	contract, _, err := m.imageContract()
	if err != nil {
		return contract, err
	}
	if err = m.verifyImageTrust(contract); err != nil {
		return contract, err
	}
	if _, err = regular(filepath.Join(m.root, "lib/apk/db/installed")); err != nil {
		return contract, errors.New("APK database is not initialized with immutable image providers")
	}
	repo := filepath.Join(m.root, "etc/apk/repositories")
	keys := filepath.Join(m.root, "etc/apk/keys")
	cache := filepath.Join(m.state, "cache")
	for _, spec := range m.imageProviderWorld(contract) {
		if err = m.runAPK(repo, keys, cache, "info", "--installed", spec); err != nil {
			return contract, fmt.Errorf("installed image does not provide exact %s", spec)
		}
	}
	return contract, nil
}

func (m *manager) restore(stage string) error {
	contract, err := m.baseReady()
	if err != nil {
		return err
	}
	checkpoint, _, err := m.validateCheckpoint(stage, imageContractPath(), true)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(m.root, "addons"))
	if err != nil || len(entries) != 0 {
		return errors.New("target image addon payload is not fresh")
	}
	packages := make([]string, 0, len(checkpoint.Packages))
	for _, pkg := range checkpoint.Packages {
		packages = append(packages, filepath.Join(stage, filepath.FromSlash(pkg.Path)))
	}
	repoFile := filepath.Join(m.root, "etc/apk/repositories")
	keysDir := filepath.Join(m.root, "etc/apk/keys")
	if len(packages) != 0 {
		args := []string{"add", "--no-network", "--no-scripts", "--no-commit-hooks", "--upgrade"}
		args = append(args, packages...)
		if err = m.runAPK(repoFile, keysDir, filepath.Join(m.state, "cache"), args...); err != nil {
			return fmt.Errorf("offline addon restore failed: %w", err)
		}
	}
	for _, intent := range checkpoint.World {
		root, _ := normalizeAddonName(intent)
		if err = m.runAPK(repoFile, keysDir, filepath.Join(m.state, "cache"), "info", "--installed", root); err != nil {
			return fmt.Errorf("offline restore did not install %s", root)
		}
	}
	world := append(m.imageProviderWorld(contract), checkpoint.World...)
	sort.Strings(world)
	return os.WriteFile(filepath.Join(m.root, "etc/apk/world"), []byte(strings.Join(world, "\n")+"\n"), 0600)
}

func (m *manager) addonPath(path string) string {
	const imageRoot = "/opt/nkos"
	if m.root == imageRoot || !strings.HasPrefix(path, imageRoot+"/") {
		return path
	}
	return filepath.Join(m.root, strings.TrimPrefix(path, imageRoot+"/"))
}

func (m *manager) loadAddon(id string, ensureState, requireExecutables bool) (addonSpec, string, error) {
	if !validID(id) {
		return addonSpec{}, "", fmt.Errorf("invalid addon id: %s", id)
	}
	dir := filepath.Join(m.root, "addons", id)
	if _, err := regular(filepath.Join(dir, "addon.json")); err != nil {
		return addonSpec{}, "", fmt.Errorf("addon %s is not installed", id)
	}
	var addon addonSpec
	if err := readJSON(filepath.Join(dir, "addon.json"), &addon); err != nil {
		return addon, "", err
	}
	if addon.ID != id || addon.Package != "nkos-addon-"+id || !apkVersionRE.MatchString(addon.Version) || addon.BaseABI == "" || addon.ServerAPI != "nkos-server-api=1" || addon.Config != "/etc/kvm/"+id || addon.Data != "/data/"+id {
		return addon, "", errors.New("addon descriptor violates the ownership contract")
	}
	if !baseABI.MatchString(addon.BaseABI) {
		return addon, "", errors.New("addon descriptor has invalid base ABI")
	}
	// Published descriptors retain the upstream version and package revision.
	// Older descriptors carry only the full APK version and remain valid.
	if addon.SourceVersion != "" || addon.Pkgver != "" || addon.Pkgrel != nil {
		if addon.SourceVersion == "" || !apkVersionRE.MatchString(addon.Pkgver) || addon.Pkgrel == nil || *addon.Pkgrel < 0 || addon.Version != fmt.Sprintf("%s-r%d", addon.Pkgver, *addon.Pkgrel) {
			return addon, "", errors.New("addon descriptor has inconsistent package revision metadata")
		}
	}
	for _, service := range addon.Services {
		if !validID(service.Name) || !commandRE.MatchString(service.Command) || !strings.HasPrefix(service.Command, "/opt/nkos/addons/"+id+"/") || (service.Restart != "never" && service.Restart != "on-update" && service.Restart != "always") {
			return addon, "", fmt.Errorf("addon %s has an invalid service descriptor", id)
		}
		if requireExecutables {
			program := strings.Fields(service.Command)
			if len(program) == 0 {
				return addon, "", errors.New("empty service command")
			}
			path := m.addonPath(program[0])
			info, err := regular(path)
			if err != nil || info.Mode()&0111 == 0 {
				return addon, "", fmt.Errorf("service %s executable is missing", service.Name)
			}
		}
	}
	if ensureState {
		for _, p := range []string{filepath.Join("/etc/kvm", id), filepath.Join("/data", id)} {
			if err := os.MkdirAll(p, 0700); err != nil {
				return addon, "", err
			}
			if info, err := os.Lstat(p); err != nil || info.Mode()&os.ModeSymlink != 0 {
				return addon, "", errors.New("addon state path is a symlink")
			}
		}
	}
	return addon, dir, nil
}

func (m *manager) addon(id string) (addonSpec, string, error) {
	return m.loadAddon(id, true, true)
}

func (m *manager) servicePID(id, name string) string { return filepath.Join(m.run, id+"."+name+".pid") }

func (m *manager) stopService(id string, service serviceSpec) error {
	path := m.servicePID(id, service.Name)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid < 2 {
		_ = os.Remove(path)
		return nil
	}
	process, err := os.FindProcess(pid)
	if err == nil {
		_ = process.Signal(syscall.SIGTERM)
	}
	for i := 0; i < 20; i++ {
		if err = syscall.Kill(pid, 0); err != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err == nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	return os.Remove(path)
}

func (m *manager) startService(id, dir string, service serviceSpec) error {
	marker := filepath.Join(m.state, "enabled", id+"."+service.Name)
	if _, err := os.Stat(marker); err != nil {
		return nil
	}
	pidPath := m.servicePID(id, service.Name)
	if data, err := os.ReadFile(pidPath); err == nil {
		if pid, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil && syscall.Kill(pid, 0) == nil {
			return nil
		}
	}
	words := strings.Fields(service.Command)
	if len(words) == 0 {
		return errors.New("empty service command")
	}
	args := append([]string{}, words[1:]...)
	program := m.addonPath(words[0])
	cmd := exec.Command(program, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NKOS_ADDON_ID="+id, "NKOS_ADDON_CONFIG=/etc/kvm/"+id, "NKOS_ADDON_DATA=/data/"+id)
	log, err := os.OpenFile(filepath.Join(m.run, id+"."+service.Name+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	cmd.Stdout, cmd.Stderr = log, log
	if err = cmd.Start(); err != nil {
		_ = log.Close()
		return err
	}
	_ = log.Close()
	return os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0600)
}

func (m *manager) stopAddon(id string, addon addonSpec) error {
	for _, service := range addon.Services {
		if err := m.stopService(id, service); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
func (m *manager) startAddon(id string, dir string, addon addonSpec) error {
	for _, service := range addon.Services {
		if err := m.startService(id, dir, service); err != nil {
			return err
		}
	}
	return nil
}

// copyTree copies only ordinary files and directories.  Addon payloads are
// deliberately link-free, so rejecting links here prevents a staged apk
// transaction from ever following a pre-existing path into the live root.
func copyTree(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("APK staging source is not a real directory")
	}
	if err = os.MkdirAll(destination, info.Mode().Perm()); err != nil {
		return err
	}
	return filepath.Walk(source, func(path string, entry os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == source {
			return nil
		}
		rel, relErr := filepath.Rel(source, path)
		if relErr != nil || rel == "." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
			return errors.New("unsafe APK staging path")
		}
		target := filepath.Join(destination, rel)
		if entry.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("APK staging source contains a symlink: %s", rel)
		}
		if entry.IsDir() {
			if err = os.MkdirAll(target, entry.Mode().Perm()); err != nil {
				return err
			}
			return os.Chmod(target, entry.Mode().Perm())
		}
		if !entry.Mode().IsRegular() {
			return fmt.Errorf("APK staging source contains a special file: %s", rel)
		}
		return copyFile(path, target, entry.Mode().Perm())
	})
}

func (m *manager) cloneAPKRoot(destination string) error {
	if _, err := regular(m.root); err == nil {
		return errors.New("APK root unexpectedly became a regular file")
	}
	if info, err := os.Lstat(m.root); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("APK root is not a real directory")
	}
	if err := os.MkdirAll(destination, 0700); err != nil {
		return err
	}
	for _, rel := range []string{"etc/apk", "lib/apk/db", "addons"} {
		source := filepath.Join(m.root, filepath.FromSlash(rel))
		target := filepath.Join(destination, filepath.FromSlash(rel))
		if _, err := os.Lstat(source); os.IsNotExist(err) {
			if err = os.MkdirAll(target, 0700); err != nil {
				return err
			}
			continue
		} else if err != nil {
			return err
		}
		if err := copyTree(source, target); err != nil {
			return err
		}
	}
	// The copied database lock belongs to the live process and must never be
	// reused by the resolver.  apk creates its own lock for the staged root.
	_ = os.Remove(filepath.Join(destination, "lib/apk/db/lock"))
	return nil
}

type normalPlan struct {
	root          string
	cache         string
	packages      []string
	installed     []installedPackage
	selected      installedPackage
	previousWorld []string
}

func (p *normalPlan) discard() { _ = os.RemoveAll(p.root) }

func packageMap(packages []installedPackage) map[string]installedPackage {
	result := make(map[string]installedPackage, len(packages))
	for _, pkg := range packages {
		result[pkg.Name] = pkg
	}
	return result
}

func (m *manager) cachedPackagePaths(cache string) (map[string]string, error) {
	entries, err := os.ReadDir(cache)
	if err != nil {
		return nil, err
	}
	paths := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".apk") {
			continue
		}
		path := filepath.Join(cache, entry.Name())
		if _, err := regular(path); err != nil {
			return nil, fmt.Errorf("unsafe APK cache entry %s", entry.Name())
		}
		dump, err := m.inspectPackageMetadata(path)
		if err != nil {
			return nil, err
		}
		if !packageName.MatchString(dump.Info.Name) || !apkVersionRE.MatchString(dump.Info.Version) {
			return nil, fmt.Errorf("invalid cached addon package %s", entry.Name())
		}
		key := dump.Info.Name + "=" + dump.Info.Version
		if previous, exists := paths[key]; exists {
			first, _, firstErr := hashFile(previous)
			second, _, secondErr := hashFile(path)
			if firstErr != nil || secondErr != nil || first != second {
				return nil, fmt.Errorf("cached package identity has conflicting bytes: %s", key)
			}
			continue
		}
		paths[key] = path
	}
	return paths, nil
}

func expectedProviderVersions(contract targetContract) map[string]string {
	providers := map[string]string{
		"nkos-base-abi":   contract.BaseABI,
		"nkos-server-api": strconv.Itoa(contract.ServerAPI),
	}
	for _, feature := range contract.Features {
		parts := strings.SplitN(feature, "=", 2)
		providers[parts[0]] = parts[1]
	}
	return providers
}

func (m *manager) validateStagedInstalled(contract targetContract, installed []installedPackage) error {
	providers := expectedProviderVersions(contract)
	seenProviders := make(map[string]bool, len(providers))
	seenAddons := make(map[string]bool)
	for _, pkg := range installed {
		if want, reserved := providers[pkg.Name]; reserved {
			if pkg.Version != want || pkg.Arch != "noarch" || pkg.Description != "virtual meta package" || len(pkg.Contents) != 0 {
				return fmt.Errorf("repository replaced immutable image provider %s", pkg.Name)
			}
			seenProviders[pkg.Name] = true
			continue
		}
		if providerNameRE.MatchString(pkg.Name) {
			return fmt.Errorf("repository supplied undeclared reserved provider %s", pkg.Name)
		}
		if !packageName.MatchString(pkg.Name) || !apkVersionRE.MatchString(pkg.Version) || pkg.Arch != "riscv64" || pkg.InstalledSize < 0 {
			return errors.New("resolved closure contains an invalid addon package")
		}
		id := strings.TrimPrefix(pkg.Name, "nkos-addon-")
		addon, _, err := m.loadAddon(id, false, true)
		if err != nil {
			return fmt.Errorf("staged addon %s is invalid: %w", id, err)
		}
		if addon.Version != pkg.Version {
			return fmt.Errorf("staged addon %s descriptor version %s differs from APK %s", id, addon.Version, pkg.Version)
		}
		if addon.BaseABI != "nkos-base-abi="+contract.BaseABI || addon.ServerAPI != "nkos-server-api="+strconv.Itoa(contract.ServerAPI) {
			return fmt.Errorf("addon %s ABI/API does not match the immutable image", id)
		}
		features := make(map[string]bool, len(contract.Features))
		for _, feature := range contract.Features {
			features[feature] = true
		}
		for _, feature := range addon.Features {
			if !featureRE.MatchString(feature) || !features[feature] {
				return fmt.Errorf("addon %s requires unavailable immutable feature %s", id, feature)
			}
		}
		seenAddons[id] = true
	}
	for name := range providers {
		if !seenProviders[name] {
			return fmt.Errorf("target resolver lost immutable provider %s", name)
		}
	}
	for _, dir := range globDirs(filepath.Join(m.root, "addons")) {
		id := filepath.Base(dir)
		if !validID(id) || !seenAddons[id] {
			return fmt.Errorf("staged addon payload has no installed package: %s", id)
		}
	}
	return nil
}

func (m *manager) preflightNormal(action, id, packageArg string, contract targetContract, previousWorld []string) (*normalPlan, error) {
	if m.scratch == "" || !filepath.IsAbs(m.scratch) || filepath.Clean(m.scratch) == "/" {
		return nil, errors.New("unsafe POSIX addon transaction scratch path")
	}
	if info, err := os.Lstat(m.scratch); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("addon transaction scratch path is a symlink")
	}
	if err := os.MkdirAll(m.scratch, 0700); err != nil {
		return nil, err
	}
	root := filepath.Join(m.scratch, fmt.Sprintf("normal-%d-%s", os.Getpid(), id))
	if err := os.RemoveAll(root); err != nil {
		return nil, err
	}
	plan := &normalPlan{root: root, previousWorld: append([]string(nil), previousWorld...)}
	fail := true
	defer func() {
		if fail {
			plan.discard()
		}
	}()
	if err := m.cloneAPKRoot(root); err != nil {
		return nil, err
	}
	plan.cache = filepath.Join(root, "cache")
	if err := os.MkdirAll(plan.cache, 0700); err != nil {
		return nil, err
	}
	resolver := *m
	resolver.root = root
	repoFile := filepath.Join(root, "etc/apk/repositories")
	keysDir := filepath.Join(root, "etc/apk/keys")
	if _, err := regular(repoFile); err != nil {
		return nil, fmt.Errorf("APK repository configuration: %w", err)
	}
	if _, err := os.Stat(filepath.Join(keysDir, "")); err != nil {
		return nil, fmt.Errorf("APK trust directory: %w", err)
	}
	before, err := resolver.installedPackages(repoFile, keysDir, plan.cache)
	if err != nil {
		return nil, fmt.Errorf("normal transaction preflight query: %w", err)
	}
	argument := packageArg
	if argument == "" {
		argument = "nkos-addon-" + id
	}
	if err = resolver.runAPK(repoFile, keysDir, plan.cache, "add", "--no-scripts", "--no-commit-hooks", "--upgrade", argument); err != nil {
		return nil, fmt.Errorf("normal transaction preflight resolver rejected package: %w", err)
	}
	after, err := resolver.installedPackages(repoFile, keysDir, plan.cache)
	if err != nil {
		return nil, fmt.Errorf("normal transaction staged query: %w", err)
	}
	if err = resolver.validateStagedInstalled(contract, after); err != nil {
		return nil, err
	}
	beforeByName, afterByName := packageMap(before), packageMap(after)
	changed := make([]installedPackage, 0)
	for name, pkg := range afterByName {
		if providerNameRE.MatchString(name) {
			continue
		}
		old, existed := beforeByName[name]
		if !existed || old.Version != pkg.Version {
			changed = append(changed, pkg)
		}
	}
	selected, ok := afterByName["nkos-addon-"+id]
	if !ok {
		return nil, fmt.Errorf("normal transaction did not install addon %s", id)
	}
	sort.Slice(changed, func(i, j int) bool { return changed[i].Name < changed[j].Name })
	cached, err := resolver.cachedPackagePaths(plan.cache)
	if err != nil {
		return nil, err
	}
	packagePaths := make([]string, 0, len(changed))
	sources := make([]packageSource, 0, len(changed))
	for _, pkg := range changed {
		key := pkg.Name + "=" + pkg.Version
		path := cached[key]
		if path == "" && filepath.IsAbs(packageArg) {
			if _, statErr := regular(packageArg); statErr == nil {
				path = packageArg
			}
		}
		if path == "" {
			return nil, fmt.Errorf("normal transaction cache omitted resolved package %s", key)
		}
		sources = append(sources, packageSource{path: path, pkg: pkg})
		packagePaths = append(packagePaths, path)
	}
	if err = resolver.verifyPackageSignatures(repoFile, keysDir, plan.cache, packagePaths); err != nil {
		return nil, err
	}
	for _, source := range sources {
		if err = resolver.validatePackageMetadataContext(source.path, source.pkg, apkValidationContext{repoFile: repoFile, keysDir: keysDir, cacheDir: plan.cache, providers: resolver.imageProviderWorld(contract)}); err != nil {
			return nil, err
		}
	}
	plan.packages, plan.installed, plan.selected = packagePaths, after, selected
	fail = false
	return plan, nil
}

func (m *manager) writeAddonWorld(path string, contract targetContract, world []string) error {
	values := append([]string{}, m.imageProviderWorld(contract)...)
	values = append(values, world...)
	sort.Strings(values)
	data := []byte(strings.Join(values, "\n") + "\n")
	tmp, err := os.CreateTemp(filepath.Dir(path), ".nkos-world-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func removeEnabledMarkers(state, id string) error {
	dir := filepath.Join(state, "enabled")
	if info, err := os.Lstat(dir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	} else if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("enabled service state is not a real directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	prefix := id + "."
	for _, entry := range entries {
		if entry.Name() == id || strings.HasPrefix(entry.Name(), prefix) {
			if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func (m *manager) transaction(action, id, packageArg string) error {
	contract, err := m.baseReady()
	if err != nil {
		return err
	}
	previousWorld, err := m.readWorld()
	if err != nil {
		return err
	}
	var addon addonSpec
	var dir string
	if _, statErr := os.Stat(filepath.Join(m.root, "addons", id, "addon.json")); statErr == nil {
		// A failed historical transaction may have removed the service binary.
		// The descriptor is still sufficient to stop its recorded PID and to
		// replace the payload safely; executable validation belongs to the new
		// staged closure below.
		addon, dir, err = m.loadAddon(id, true, false)
		if err != nil {
			return err
		}
	}
	if action == "del" && dir == "" {
		return fmt.Errorf("addon %s is not installed", id)
	}
	packageName := packageArg
	if packageName == "" {
		packageName = "nkos-addon-" + id
	}
	if !packageNameRE(packageName, id) {
		return errors.New("package argument does not name the requested addon")
	}
	oldAddon, oldDir := addon, dir
	if action == "del" {
		if err = m.stopAddon(id, addon); err != nil {
			return err
		}
		err = m.runAPK(filepath.Join(m.root, "etc/apk/repositories"), filepath.Join(m.root, "etc/apk/keys"), filepath.Join(m.state, "cache"), "del", "--no-scripts", "--no-commit-hooks", packageName)
		if err != nil {
			_ = m.startAddon(id, dir, addon)
			return err
		}
		remaining := make([]string, 0, len(previousWorld))
		for _, intent := range previousWorld {
			root, _ := normalizeAddonName(intent)
			if root != "nkos-addon-"+id {
				remaining = append(remaining, intent)
			}
		}
		if err = removeEnabledMarkers(m.state, id); err != nil {
			return err
		}
		payload := filepath.Join(m.root, "addons", id)
		if info, statErr := os.Lstat(payload); statErr == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return errors.New("addon payload path is a symlink")
			}
			if err = os.RemoveAll(payload); err != nil {
				return err
			}
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
		return m.writeAddonWorld(filepath.Join(m.root, "etc/apk/world"), contract, remaining)
	}

	// Resolve and fully validate the resulting closure in a throwaway APK
	// root.  No service is stopped and no live database/payload is touched
	// until this succeeds.
	plan, err := m.preflightNormal(action, id, packageName, contract, previousWorld)
	if err != nil {
		return err
	}
	defer plan.discard()
	if dir != "" {
		if err = m.stopAddon(id, addon); err != nil {
			return err
		}
	}
	if len(plan.packages) != 0 {
		commitArgs := []string{"add", "--no-network", "--no-scripts", "--no-commit-hooks", "--upgrade"}
		commitArgs = append(commitArgs, plan.packages...)
		err = m.runAPK(filepath.Join(m.root, "etc/apk/repositories"), filepath.Join(m.root, "etc/apk/keys"), filepath.Join(m.state, "cache"), commitArgs...)
		if err != nil {
			if dir != "" {
				_ = m.startAddon(id, dir, addon)
			}
			return fmt.Errorf("offline addon commit failed after preflight: %w", err)
		}
	}
	addon, dir, err = m.addon(id)
	if err != nil {
		if oldDir != "" {
			_ = m.startAddon(id, oldDir, oldAddon)
		}
		return fmt.Errorf("installed addon failed post-commit validation: %w", err)
	}
	if addon.BaseABI != "nkos-base-abi="+contract.BaseABI || addon.ServerAPI != "nkos-server-api="+strconv.Itoa(contract.ServerAPI) {
		if oldDir != "" {
			_ = m.startAddon(id, oldDir, oldAddon)
		}
		return errors.New("addon ABI/API does not match the immutable image")
	}
	updatedWorld := replaceAddonIntent(previousWorld, packageName, id, plan.selected.Version)
	if err = m.writeAddonWorld(filepath.Join(m.root, "etc/apk/world"), contract, updatedWorld); err != nil {
		if oldDir != "" {
			_ = m.startAddon(id, oldDir, oldAddon)
		}
		return fmt.Errorf("cannot persist addon world: %w", err)
	}
	if action == "upgrade" && dir != "" {
		return m.startAddon(id, dir, addon)
	}
	return nil
}

func replaceAddonIntent(previous []string, argument, id, selectedVersion string) []string {
	name := "nkos-addon-" + id
	result := make([]string, 0, len(previous)+1)
	var old string
	for _, intent := range previous {
		root, _ := normalizeAddonName(intent)
		if root == name {
			old = intent
			continue
		}
		result = append(result, intent)
	}
	argument = strings.TrimSpace(strings.SplitN(argument, "#", 2)[0])
	intent := name + "=" + selectedVersion
	if strings.HasPrefix(argument, name+"=") {
		constraint := strings.TrimPrefix(argument, name+"=")
		if strings.HasPrefix(constraint, ">=") || strings.HasPrefix(constraint, "<=") || strings.HasPrefix(constraint, ">") || strings.HasPrefix(constraint, "<") || strings.HasPrefix(constraint, "~") {
			operator := constraint[:1]
			if len(constraint) > 1 && constraint[1] == '=' {
				operator += "="
			}
			intent = name + operator + selectedVersion
		}
	} else if argument == name && old != "" {
		constraint := strings.TrimPrefix(old, name)
		if strings.HasPrefix(constraint, ">=") || strings.HasPrefix(constraint, "<=") || strings.HasPrefix(constraint, ">") || strings.HasPrefix(constraint, "<") || strings.HasPrefix(constraint, "~") {
			intent = name + constraint
		}
	}
	result = append(result, intent)
	sort.Strings(result)
	return result
}

func packageNameRE(value, id string) bool {
	if value == "nkos-addon-"+id {
		return true
	}
	if strings.HasPrefix(value, "nkos-addon-"+id+"=") {
		return apkVersionRE.MatchString(strings.TrimPrefix(value, "nkos-addon-"+id+"="))
	}
	return filepath.Clean(value) == value && filepath.Dir(value) == "/data/.nkos-apk/incoming" &&
		strings.HasPrefix(filepath.Base(value), "nkos-addon-"+id+"-") && strings.HasSuffix(value, ".apk")
}

func (m *manager) status() error {
	result := map[string]any{"root": m.root, "world": "missing", "database": "missing", "contract": "missing", "addons": []string{}, "addon_versions": map[string]string{}}
	if _, err := os.Stat(filepath.Join(m.root, "etc/apk/world")); err == nil {
		result["world"] = "ready"
	}
	if _, err := os.Stat(filepath.Join(m.root, "lib/apk/db/installed")); err == nil {
		result["database"] = "ready"
	}
	if _, err := os.Stat(imageContractPath()); err == nil {
		result["contract"] = "ready"
	}
	var addons []string
	for _, dir := range globDirs(filepath.Join(m.root, "addons")) {
		if validID(filepath.Base(dir)) {
			if _, err := regular(filepath.Join(dir, "addon.json")); err == nil {
				addons = append(addons, filepath.Base(dir))
			}
		}
	}
	sort.Strings(addons)
	result["addons"] = addons
	result["addon_versions"] = m.installedAddonVersions()
	data, _ := json.Marshal(result)
	_, _ = os.Stdout.Write(append(data, '\n'))
	return nil
}

func (m *manager) installedAddonVersions() map[string]string {
	versions := map[string]string{}
	repo := filepath.Join(m.root, "etc/apk/repositories")
	keys := filepath.Join(m.root, "etc/apk/keys")
	packages, err := m.installedPackages(repo, keys, filepath.Join(m.state, "cache"))
	if err != nil {
		return versions
	}
	for _, pkg := range packages {
		if packageName.MatchString(pkg.Name) && apkVersionRE.MatchString(pkg.Version) {
			versions[strings.TrimPrefix(pkg.Name, "nkos-addon-")] = pkg.Version
		}
	}
	return versions
}

func globDirs(path string) []string {
	entries, _ := os.ReadDir(path)
	var result []string
	for _, e := range entries {
		if e.IsDir() {
			result = append(result, filepath.Join(path, e.Name()))
		}
	}
	return result
}

func (m *manager) runAddon(id string, args []string) error {
	if len(args) < 2 || args[0] != "--" {
		return errors.New("run requires -- before the program")
	}
	_, dir, err := m.addon(id)
	if err != nil {
		return err
	}
	program := args[1]
	if !strings.HasPrefix(program, dir+string(os.PathSeparator)) || strings.Contains(program, "..") {
		return errors.New("program must be below the addon payload")
	}
	info, err := regular(program)
	if err != nil || info.Mode()&0111 == 0 {
		return errors.New("program is not executable")
	}
	cmd := exec.Command(program, args[2:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NKOS_ADDON_ID="+id, "NKOS_ADDON_CONFIG=/etc/kvm/"+id, "NKOS_ADDON_DATA=/data/"+id)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err = cmd.Run()
	if exit, ok := err.(*exec.ExitError); ok {
		return fmt.Errorf("addon exited with status %d", exit.ExitCode())
	}
	return err
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: nkos-addons status | list | add ID [PACKAGE] | upgrade ID [PACKAGE] | del ID | enable ID | disable ID | start ID | stop ID | restart ID | run ID -- PROGRAM [ARGS...] | prepare-upgrade CONTRACT STAGE | verify-upgrade CONTRACT STAGE | restore-upgrade STAGE`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(exUsage)
	}
	m := newManager()
	command := os.Args[1]
	args := os.Args[2:]
	if command == "verify-upgrade" {
		if os.Geteuid() != 0 {
			die(exUsage, "must run as root")
		}
	} else {
		m.requireRoot()
	}
	switch command {
	case "status":
		if len(args) != 0 {
			usage()
			os.Exit(exUsage)
		}
		if err := m.status(); err != nil {
			die(exPolicy, "%v", err)
		}
	case "list":
		if len(args) != 0 {
			usage()
			os.Exit(exUsage)
		}
		versions := m.installedAddonVersions()
		for _, dir := range globDirs(filepath.Join(m.root, "addons")) {
			if validID(filepath.Base(dir)) {
				if _, err := regular(filepath.Join(dir, "addon.json")); err == nil {
					id := filepath.Base(dir)
					if version := versions[id]; version != "" {
						fmt.Printf("%s %s\n", id, version)
					} else {
						fmt.Println(id)
					}
				}
			}
		}
	case "prepare-upgrade":
		if len(args) != 2 {
			usage()
			os.Exit(exUsage)
		}
		unlock := m.lock(true)
		defer unlock()
		if err := m.prepare(args[0], args[1]); err != nil {
			die(exPolicy, "%v", err)
		}
	case "restore-upgrade":
		if len(args) != 1 {
			usage()
			os.Exit(exUsage)
		}
		unlock := m.lock(false)
		defer unlock()
		if err := m.restore(args[0]); err != nil {
			die(exAPK, "%v", err)
		}
	case "verify-upgrade":
		if len(args) != 2 {
			usage()
			os.Exit(exUsage)
		}
		if err := m.verifyUpgrade(args[0], args[1]); err != nil {
			die(exPolicy, "%v", err)
		}
	case "add", "install", "upgrade", "update", "del", "remove":
		if len(args) < 1 || len(args) > 2 {
			usage()
			os.Exit(exUsage)
		}
		id := args[0]
		packageArg := ""
		if len(args) == 2 {
			packageArg = args[1]
		}
		action := command
		if action == "install" {
			action = "add"
		}
		if action == "update" {
			action = "upgrade"
		}
		if action == "remove" {
			action = "del"
		}
		unlock := m.lock(false)
		defer unlock()
		if err := m.transaction(action, id, packageArg); err != nil {
			die(exPolicy, "%v", err)
		}
	case "enable", "disable", "start", "stop", "restart":
		if len(args) != 1 {
			usage()
			os.Exit(exUsage)
		}
		id := args[0]
		addon, dir, err := m.addon(id)
		if err != nil {
			die(exPolicy, "%v", err)
		}
		unlock := m.lock(false)
		defer unlock()
		switch command {
		case "enable":
			if err = os.MkdirAll(filepath.Join(m.state, "enabled"), 0700); err == nil {
				for _, service := range addon.Services {
					err = os.WriteFile(filepath.Join(m.state, "enabled", id+"."+service.Name), []byte("enabled\n"), 0600)
					if err != nil {
						break
					}
				}
			}
			if err == nil {
				err = m.startAddon(id, dir, addon)
			}
		case "disable":
			for _, service := range addon.Services {
				_ = os.Remove(filepath.Join(m.state, "enabled", id+"."+service.Name))
			}
			err = m.stopAddon(id, addon)
		case "start":
			err = m.startAddon(id, dir, addon)
		case "stop":
			err = m.stopAddon(id, addon)
		case "restart":
			err = m.stopAddon(id, addon)
			if err == nil {
				err = m.startAddon(id, dir, addon)
			}
		}
		if err != nil {
			die(exLifecycle, "%v", err)
		}
	case "run":
		if len(args) < 3 {
			usage()
			os.Exit(exUsage)
		}
		if err := m.runAddon(args[0], args[1:]); err != nil {
			die(exPolicy, "%v", err)
		}
	default:
		usage()
		os.Exit(exUsage)
	}
}
