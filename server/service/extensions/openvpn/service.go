package openvpn

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"NanoKVM-Server/proto"
	"github.com/gin-gonic/gin"
)

var idPattern = regexp.MustCompile(`^ov[0-9a-f]{10}$`)

type Profile struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	NeedsAuth       bool   `json:"needsAuth"`
	NeedsPassphrase bool   `json:"needsPassphrase"`
}
type Status struct {
	Profile
	State            string `json:"state"`
	Enabled          bool   `json:"enabled"`
	CredentialsSaved bool   `json:"credentialsSaved"`
	Address          string `json:"address"`
	Received         uint64 `json:"received"`
	Sent             uint64 `json:"sent"`
	DCO              bool   `json:"dco"`
	Error            string `json:"error,omitempty"`
}
type Service struct {
	mu       sync.Mutex
	dir, run string
}

func NewService() *Service {
	s := &Service{dir: "/etc/kvm/openvpn", run: "/run/nkos-openvpn"}
	go func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		id := s.desired()
		if id != "" {
			ps, _ := s.profiles()
			for _, p := range ps {
				if p.ID == id && s.pid(id) == 0 {
					_ = s.start(p)
				}
			}
		}
	}()

	return s
}
func writePrivate(path string, data []byte) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".pending-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	_, e = f.Write(data)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func (s *Service) profiles() ([]Profile, error) {
	ps := []Profile{}
	b, e := os.ReadFile(filepath.Join(s.dir, "profiles.json"))
	if errors.Is(e, os.ErrNotExist) {
		return ps, nil
	}
	if e != nil {
		return nil, e
	}
	if e = json.Unmarshal(b, &ps); e != nil {
		return nil, e
	}
	for _, p := range ps {
		if !idPattern.MatchString(p.ID) {
			return nil, fmt.Errorf("invalid profile index")
		}
	}
	return ps, nil
}
func (s *Service) save(ps []Profile) error {
	b, e := json.Marshal(ps)
	if e != nil {
		return e
	}
	return writePrivate(filepath.Join(s.dir, "profiles.json"), b)
}
func (s *Service) path(id, suffix string) string    { return filepath.Join(s.dir, id+suffix) }
func (s *Service) runtime(id, suffix string) string { return filepath.Join(s.run, id+suffix) }
func (s *Service) desired() string {
	b, _ := os.ReadFile(filepath.Join(s.dir, "active"))
	id := strings.TrimSpace(string(b))
	if idPattern.MatchString(id) {
		return id
	}
	return ""
}
func (s *Service) pid(id string) int {
	b, _ := os.ReadFile(s.runtime(id, ".pid"))
	pid, e := strconv.Atoi(strings.TrimSpace(string(b)))
	if e != nil || pid < 2 {
		return 0
	}
	cmd, e := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if e != nil {
		return 0
	}
	args := strings.Split(string(cmd), "\x00")
	if len(args) < 2 || filepath.Base(args[0]) != "nkos-openvpn3" {
		return 0
	}
	for i, a := range args {
		if a == "--config" && i+1 < len(args) && args[i+1] == s.path(id, ".ovpn") {
			return pid
		}
	}
	return 0
}
func (s *Service) credentials(p Profile) bool {
	for suffix, needed := range map[string]bool{".auth": p.NeedsAuth, ".pass": p.NeedsPassphrase} {
		if needed {
			if _, e := os.Stat(s.path(p.ID, suffix)); e != nil {
				return false
			}
		}
	}
	return true
}
func (s *Service) start(p Profile) error {
	if !s.credentials(p) {
		return fmt.Errorf("credentials are required")
	}
	if e := os.MkdirAll(s.run, 0700); e != nil {
		return e
	}
	// Validate persisted profiles again before handing them to a root daemon.
	b, e := os.ReadFile(s.path(p.ID, ".ovpn"))
	if e != nil {
		return e
	}
	if _, e = Normalize(string(b), nil); e != nil {
		return e
	}
	if _, e = os.Stat("/run/resolvconf/interfaces/nkos.base"); os.IsNotExist(e) {
		base, err := os.ReadFile("/etc/resolv.conf")
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "resolvconf", "-a", "nkos.base")
		cmd.Stdin = strings.NewReader(string(base))
		if e = cmd.Run(); e != nil {
			return e
		}
	}
	args := []string{"--config", s.path(p.ID, ".ovpn"), "--status", s.runtime(p.ID, ".json")}
	if p.NeedsAuth {
		args = append(args, "--auth", s.path(p.ID, ".auth"))
	}
	if p.NeedsPassphrase {
		args = append(args, "--passphrase", s.path(p.ID, ".pass"))
	}
	_ = os.Remove(s.runtime(p.ID, ".json"))
	_ = os.Remove(s.runtime(p.ID, ".pid"))
	cmd := exec.Command("nkos-openvpn3", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if e = cmd.Start(); e != nil {
		return fmt.Errorf("OpenVPN 3 could not start")
	}
	go func() { _ = cmd.Wait() }()
	if e = writePrivate(s.runtime(p.ID, ".pid"), []byte(strconv.Itoa(cmd.Process.Pid)+"\n")); e != nil {
		_ = cmd.Process.Kill()
		return e
	}
	return nil
}

func (s *Service) stop(id string) error {
	pid := s.pid(id)
	if pid == 0 {
		return nil
	}
	if e := syscall.Kill(pid, syscall.SIGTERM); e != nil && !errors.Is(e, syscall.ESRCH) {
		return e
	}
	for n := 0; n < 50; n++ {
		if s.pid(id) == 0 {
			return s.clearDNS()
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Check ownership again before escalation; never signal a reused PID.
	if s.pid(id) == pid {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	for n := 0; n < 10; n++ {
		if s.pid(id) == 0 {
			return s.clearDNS()
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("OpenVPN has not stopped")
}
func (s *Service) clearDNS() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "resolvconf", "-d", "nkos.openvpn", "-f").Run()
}
func (s *Service) GetStatus(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var rsp proto.Response
	ps, e := s.profiles()
	if e != nil {
		rsp.ErrRsp(c, -1, "cannot read OpenVPN profiles")
		return
	}
	states := make([]Status, 0, len(ps))
	wanted := s.desired()
	_, toolErr := exec.LookPath("nkos-openvpn3")
	for _, p := range ps {
		st := Status{Profile: p, State: "off", Enabled: wanted == p.ID, CredentialsSaved: s.credentials(p)}
		var current struct {
			State    string `json:"state"`
			Error    string `json:"error"`
			Address  string `json:"address"`
			Received uint64 `json:"received"`
			Sent     uint64 `json:"sent"`
			DCO      bool   `json:"dco"`
		}
		b, _ := os.ReadFile(s.runtime(p.ID, ".json"))
		_ = json.Unmarshal(b, &current)
		if s.pid(p.ID) > 0 {
			st.State = "connecting"
			switch current.State {
			case "connected", "connecting", "reconnecting", "authenticating", "error":
				st.State = current.State
			}
			st.Address = current.Address
			st.Received = current.Received
			st.Sent = current.Sent
			st.DCO = current.DCO
			st.Error = current.Error
		} else if st.Enabled {
			st.State = "error"
			st.Error = "OpenVPN stopped; check the profile and credentials"
			if current.Error != "" {
				st.Error = current.Error
			}
		}

		states = append(states, st)
	}
	rsp.OkRspWithData(c, gin.H{"available": toolErr == nil, "profiles": states})
}
func (s *Service) Import(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var rsp proto.Response
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 5*1024*1024)
	form, e := c.MultipartForm()
	if e != nil {
		rsp.ErrRsp(c, -1, "upload .ovpn profiles and their referenced certificate/key files")
		return
	}
	defer form.RemoveAll()
	files := form.File["files"]
	if len(files) == 0 || len(files) > 64 {
		rsp.ErrRsp(c, -1, "upload 1 to 64 files")
		return
	}
	assets := map[string]string{}
	names := []string{}
	for _, f := range files {
		name := filepath.Base(f.Filename)
		if len(name) > 128 {
			rsp.ErrRsp(c, -1, "filename too long")
			return
		}
		if _, ok := assets[name]; ok {
			rsp.ErrRsp(c, -1, "duplicate filename in upload")
			return
		}
		stream, err := f.Open()
		if err != nil {
			rsp.ErrRsp(c, -1, "cannot read upload")
			return
		}
		b, err := io.ReadAll(io.LimitReader(stream, 256*1024+1))
		stream.Close()
		if err != nil || len(b) > 256*1024 {
			rsp.ErrRsp(c, -1, "maximum file size is 256 KiB")
			return
		}
		assets[name] = string(b)
		if strings.HasSuffix(strings.ToLower(name), ".ovpn") {
			names = append(names, name)
		}
	}
	ps, e := s.profiles()
	if e != nil || len(names) == 0 || len(ps)+len(names) > 16 {
		rsp.ErrRsp(c, -1, "maximum 16 OpenVPN profiles")
		return
	}
	type pending struct {
		p      Profile
		config string
	}
	batch := []pending{}
	for _, name := range names {
		cfg, err := Normalize(assets[name], assets)
		if err != nil {
			rsp.ErrRsp(c, -1, err.Error())
			return
		}
		var random [5]byte
		if _, err = rand.Read(random[:]); err != nil {
			rsp.ErrRsp(c, -1, "cannot create profile")
			return
		}
		batch = append(batch, pending{Profile{"ov" + hex.EncodeToString(random[:]), strings.TrimSuffix(name, filepath.Ext(name)), cfg.NeedsAuth, strings.Contains(cfg.Config, "ENCRYPTED PRIVATE KEY") || strings.Contains(cfg.Config, "Proc-Type: 4,ENCRYPTED")}, cfg.Config})
	}
	committed := false
	defer func() {
		if !committed {
			for _, p := range batch {
				_ = os.Remove(s.path(p.p.ID, ".ovpn"))
			}
		}
	}()
	for _, p := range batch {
		if e = writePrivate(s.path(p.p.ID, ".ovpn"), []byte(p.config)); e != nil {
			rsp.ErrRsp(c, -1, "cannot save profile")
			return
		}
		ps = append(ps, p.p)
	}
	if e = s.save(ps); e != nil {
		rsp.ErrRsp(c, -1, "cannot save profile index")
		return
	}
	committed = true
	rsp.OkRsp(c)
}
func (s *Service) Change(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var rsp proto.Response
	var req struct {
		ID         string `json:"id"`
		Action     string `json:"action"`
		Username   string `json:"username"`
		Password   string `json:"password"`
		Passphrase string `json:"passphrase"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
	if c.ShouldBindJSON(&req) != nil || !idPattern.MatchString(req.ID) {
		rsp.ErrRsp(c, -1, "invalid profile")
		return
	}
	ps, e := s.profiles()
	if e != nil {
		rsp.ErrRsp(c, -1, "cannot read profiles")
		return
	}
	idx := -1
	for i, p := range ps {
		if p.ID == req.ID {
			idx = i
		}
	}
	if idx < 0 {
		rsp.ErrRsp(c, -1, "profile not found")
		return
	}
	p := ps[idx]
	switch req.Action {
	case "credentials":
		for _, v := range []string{req.Username, req.Password, req.Passphrase} {
			if len(v) > 4096 || strings.ContainsAny(v, "\r\n\x00") {
				rsp.ErrRsp(c, -1, "invalid credentials")
				return
			}
		}
		if p.NeedsPassphrase && req.Passphrase == "" {
			rsp.ErrRsp(c, -1, "private-key passphrase is required")
			return
		}
		if p.NeedsAuth {
			if req.Username == "" || req.Password == "" {
				rsp.ErrRsp(c, -1, "username and password are required")
				return
			}
			e = writePrivate(s.path(p.ID, ".auth"), []byte(req.Username+"\n"+req.Password+"\n"))
		}
		if e == nil && p.NeedsPassphrase {
			if req.Passphrase == "" {
				rsp.ErrRsp(c, -1, "private-key passphrase is required")
				return
			}
			e = writePrivate(s.path(p.ID, ".pass"), []byte(req.Passphrase+"\n"))
		}
	case "up":
		for _, other := range ps {
			if other.ID != p.ID && (s.pid(other.ID) > 0 || s.desired() == other.ID) {
				rsp.ErrRsp(c, -1, "disable the active OpenVPN profile before switching")
				return
			}
		}
		if s.pid(p.ID) == 0 {
			e = s.start(p)
		}
		if e == nil {
			e = writePrivate(filepath.Join(s.dir, "active"), []byte(p.ID+"\n"))
			if e != nil {
				_ = s.stop(p.ID)
			}
		}
	case "down", "delete":
		wasDesired := s.desired() == p.ID
		if wasDesired {
			e = os.Remove(filepath.Join(s.dir, "active"))
		}
		if e == nil {
			e = s.stop(p.ID)
		}
		if e == nil && wasDesired {
			e = s.clearDNS()
		}
		if e == nil && req.Action == "delete" {
			e = s.save(append(ps[:idx:idx], ps[idx+1:]...))
			if e == nil {
				for _, suffix := range []string{".ovpn", ".auth", ".pass"} {
					_ = os.Remove(s.path(p.ID, suffix))
				}
			}
		}
	default:
		rsp.ErrRsp(c, -1, "invalid action")
		return
	}
	if e != nil {
		message := "OpenVPN operation failed"
		if e.Error() == "credentials are required" {
			message = e.Error()
		}
		rsp.ErrRsp(c, -1, message)
		return
	}
	rsp.OkRsp(c)
}
