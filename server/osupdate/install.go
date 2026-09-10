package osupdate

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

const Base = "/kvmapp/.os-update"
const Helper = "/usr/sbin/nkos-update"

type Installed struct {
	Version  string `json:"version"`
	Sequence uint64 `json:"sequence"`
}
type Result struct {
	State   string `json:"state"`
	Message string `json:"message"`
	Version string `json:"version,omitempty"`
	ID      string `json:"id,omitempty"`
}
type transaction struct {
	OldVersion   string
	OldInstalled Installed
	Phase        string
}

func writeJSON(name string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return writeAtomic(name, b, 0600)
}
func writeAtomic(name string, b []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(name), ".write-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err != nil {
		return err
	}
	if ce != nil {
		return ce
	}
	if err = os.Rename(tmp, name); err != nil {
		return err
	}
	return syncDir(filepath.Dir(name))
}
func syncDir(name string) error {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func syncTreeDirs(root string) error {
	return filepath.WalkDir(root, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return syncDir(name)
		}
		return nil
	})
}
func readJSON(name string, v any) error {
	b, err := os.ReadFile(name)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
func GetInstalled() Installed {
	var i Installed
	if readJSON(Base+"/installed.json", &i) != nil {
		v, _ := os.ReadFile("/kvmapp/version")
		i.Version = strings.TrimSpace(string(v))
	}
	return i
}
func GetResult() Result {
	var r Result
	if readJSON(Base+"/result.json", &r) != nil {
		r.State = "idle"
	}
	return r
}
func SetResult(r Result) error { return writeJSON(Base+"/result.json", r) }
func Lock() (*os.File, error) {
	if err := os.MkdirAll(Base, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(Base+"/lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("another update operation is in progress")
	}
	return f, nil
}
func compatible(b *Bundle) error {
	marker, err := os.ReadFile("/etc/nanokvm-buildroot")
	if err != nil || !strings.Contains(string(marker), "flavour=enhanced") {
		return errors.New("NanoKVM OS system image required")
	}
	abi, err := NativeABI("/kvmapp/server/dl_lib")
	if err != nil {
		return err
	}
	if abi != b.Manifest.NativeABI {
		return errors.New("this application requires a different system image; use a compatible NanoKVM OS image")
	}
	if b.Manifest.Sequence <= GetInstalled().Sequence {
		return errors.New("this update is already installed or older than the installed release")
	}
	return nil
}
func ValidateForDevice(file string) (*Bundle, error) {
	b, err := Verify(file)
	if err != nil {
		return nil, err
	}
	if err = compatible(b); err != nil {
		return nil, err
	}
	return b, nil
}
func PackagePath(id string) (string, error) {
	if !digestRE.MatchString(id) {
		return "", errors.New("invalid package identifier")
	}
	return filepath.Join(Base, id+".nkos"), nil
}
func Prepare(file string) (*Bundle, error) {
	b, err := ValidateForDevice(file)
	if err != nil {
		return nil, err
	}
	dest := Base + "/validation"
	os.RemoveAll(dest)
	if err = Extract(file, b, dest); err != nil {
		return nil, err
	}
	if err = os.RemoveAll(dest); err != nil {
		return nil, err
	}
	target, _ := PackagePath(b.ID)
	if file != target {
		if err = os.Rename(file, target); err != nil {
			return nil, err
		}
	}
	if err = syncDir(Base); err != nil {
		return nil, err
	}
	// Only one prepared package is needed. Never accumulate old downloads on SD.
	entries, _ := os.ReadDir(Base)
	for _, entry := range entries {
		name := entry.Name()
		if len(name) == 69 && strings.HasSuffix(name, ".nkos") && digestRE.MatchString(name[:64]) && name != b.ID+".nkos" {
			os.Remove(filepath.Join(Base, name))
		}
	}
	if err = SetResult(Result{State: "prepared", ID: b.ID, Version: b.Manifest.Version, Message: "Signature, compatibility and file checks passed"}); err != nil {
		return nil, err
	}
	return b, nil
}
func service(action string) error {
	cmd := exec.Command("/etc/init.d/S95nanokvm", action)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("application %s failed: %s", action, strings.TrimSpace(string(out)))
	}
	return nil
}
func exists(p string) bool { _, err := os.Lstat(p); return err == nil }
func rollback(tx transaction) error {
	if exists(Base + "/previous") {
		if exists("/kvmapp/server") {
			if err := os.RemoveAll(Base + "/failed"); err != nil {
				return err
			}
			if err := os.Rename("/kvmapp/server", Base+"/failed"); err != nil {
				return err
			}
		}
		if err := os.Rename(Base+"/previous", "/kvmapp/server"); err != nil {
			return err
		}
	}
	if !exists("/kvmapp/server") {
		return errors.New("application recovery requires a system image")
	}
	if err := syncDir("/kvmapp"); err != nil {
		return err
	}
	if err := syncDir(Base); err != nil {
		return err
	}
	if err := writeAtomic("/kvmapp/version", []byte(tx.OldVersion), 0644); err != nil {
		return err
	}
	if err := writeJSON(Base+"/installed.json", tx.OldInstalled); err != nil {
		return err
	}
	if err := os.Remove(Base + "/transaction.json"); err != nil && !os.IsNotExist(err) {
		return err
	}
	return syncDir(Base)
}

// Recover runs before S95 at boot. An uncommitted transaction returns to old app.
func Recover() error {
	var tx transaction
	if err := readJSON(Base+"/transaction.json", &tx); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if tx.Phase == "committed" {
		return os.Remove(Base + "/transaction.json")
	}
	if err := rollback(tx); err != nil {
		return err
	}
	return SetResult(Result{State: "rolled-back", Message: "Interrupted update recovered; previous application restored"})
}
func healthy() bool {
	// Use the same authenticated loopback route as internal clients. Never
	// follow the HTTPS redirect or disable certificate verification.
	token, err := os.ReadFile("/etc/kvm/.picoclaw_internal_token")
	if err != nil {
		return false
	}
	var cfg struct {
		Port struct {
			HTTP int `yaml:"http"`
		} `yaml:"port"`
	}
	cfg.Port.HTTP = 80
	raw, err := os.ReadFile("/etc/kvm/server.yaml")
	if err != nil || yaml.Unmarshal(raw, &cfg) != nil || cfg.Port.HTTP < 1 || cfg.Port.HTTP > 65535 {
		return false
	}
	req, err := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/api/os/update/health", cfg.Port.HTTP), nil)
	if err != nil {
		return false
	}
	req.Header.Set("X-NanoKVM-Internal-Token", strings.TrimSpace(string(token)))
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Install is called only by the independent helper while holding the update lock.
func Install(id string) (err error) {
	// Report failures before a transaction exists as well as rollback failures.
	defer func() {
		if err != nil {
			r := GetResult()
			if r.State != "rolled-back" && r.State != "failed" {
				SetResult(Result{State: "failed", Message: err.Error()})
			}
		}
	}()
	file, err := PackagePath(id)
	if err != nil {
		return err
	}
	b, err := ValidateForDevice(file)
	if err != nil {
		return err
	}
	if b.ID != id {
		return errors.New("package changed after validation")
	}
	if exists(Base + "/transaction.json") {
		return errors.New("previous update needs recovery")
	}
	info, err := os.Lstat("/kvmapp/server")
	if err != nil || !info.IsDir() {
		return errors.New("unexpected application layout")
	}
	stage := Base + "/next"
	os.RemoveAll(stage)
	if err = Extract(file, b, stage); err != nil {
		return err
	}
	if err = os.Mkdir(stage+"/dl_lib", 0755); err != nil {
		return err
	}
	libs, err := os.ReadDir("/kvmapp/server/dl_lib")
	if err != nil {
		return err
	}
	for _, lib := range libs {
		if err = os.Link("/kvmapp/server/dl_lib/"+lib.Name(), stage+"/dl_lib/"+lib.Name()); err != nil {
			return err
		}
	}
	// Resolve shared libraries without running application constructors or main.
	cmd := exec.Command("/lib/ld-musl-riscv64.so.1", "--library-path", stage+"/dl_lib", "--list", stage+"/NanoKVM-Server")
	if out, e := cmd.CombinedOutput(); e != nil {
		return fmt.Errorf("application linkage failed: %s", out)
	}
	if err = os.RemoveAll(Base + "/previous"); err != nil {
		return err
	}
	if err = syncTreeDirs(stage); err != nil {
		return err
	}
	old, _ := os.ReadFile("/kvmapp/version")
	tx := transaction{OldVersion: string(old), OldInstalled: GetInstalled(), Phase: "pending"}
	if err = writeJSON(Base+"/transaction.json", tx); err != nil {
		return err
	}
	SetResult(Result{State: "installing", Version: b.Manifest.Version, ID: id, Message: "Restarting application"})
	defer func() {
		if err != nil {
			stopErr := service("stop")
			if stopErr != nil {
				SetResult(Result{State: "failed", Message: "Update recovery requires reboot: application could not stop"})
				return
			}
			re := rollback(tx)
			if re == nil {
				re = service("start")
			}
			message := err.Error()
			if re != nil {
				message += "; recovery: " + re.Error()
			}
			SetResult(Result{State: "rolled-back", Message: message})
		}
	}()
	if err = service("stop"); err != nil {
		return err
	}
	if err = os.Rename("/kvmapp/server", Base+"/previous"); err != nil {
		return err
	}
	if err = syncDir("/kvmapp"); err != nil {
		return err
	}
	if err = syncDir(Base); err != nil {
		return err
	}
	if err = os.Rename(stage, "/kvmapp/server"); err != nil {
		return err
	}
	if err = syncDir("/kvmapp"); err != nil {
		return err
	}
	if err = syncDir(Base); err != nil {
		return err
	}
	if err = service("start"); err != nil {
		return err
	}
	successes := 0
	for n := 0; n < 30; n++ {
		time.Sleep(time.Second)
		if healthy() {
			successes++
		} else {
			successes = 0
		}
		if successes >= 5 {
			break
		}
	}
	if successes < 5 {
		return errors.New("new application did not become healthy")
	}
	if err = writeAtomic("/kvmapp/version", []byte(b.Manifest.Version+"\n"), 0644); err != nil {
		return err
	}
	if err = writeJSON(Base+"/installed.json", Installed{Version: b.Manifest.Version, Sequence: b.Manifest.Sequence}); err != nil {
		return err
	}
	tx.Phase = "committed"
	if err = writeJSON(Base+"/transaction.json", tx); err != nil {
		return err
	}
	// A failure to remove an already committed journal must not roll back success.
	os.Remove(Base + "/transaction.json")
	syncDir(Base)
	SetResult(Result{State: "installed", Version: b.Manifest.Version, ID: id, Message: "Application update installed"})
	return nil
}
