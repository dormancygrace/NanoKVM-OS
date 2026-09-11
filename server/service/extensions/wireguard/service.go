package wireguard

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
	"unicode"
	"unicode/utf8"

	"NanoKVM-Server/proto"
	"github.com/gin-gonic/gin"
)

const profileDir = "/etc/kvm/wireguard"

var validID = regexp.MustCompile(`^nk[0-9a-f]{10}$`)

type Profile struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	RouteAllowedIPs bool   `json:"routeAllowedIPs"`
}
type Status struct {
	Profile
	State         string `json:"state"`
	Enabled       bool   `json:"enabled"`
	Address       string `json:"address"`
	LastHandshake int64  `json:"lastHandshake"`
	Received      uint64 `json:"received"`
	Sent          uint64 `json:"sent"`
	Error         string `json:"error,omitempty"`
}
type Service struct {
	mu       sync.Mutex
	dir      string
	failures map[string]string
}

func NewService() *Service {
	s := &Service{dir: profileDir, failures: map[string]string{}}
	go s.restore()
	return s
}

// Configs contain credentials. They never appear in status responses, logs or argv.
func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (s *Service) profiles() ([]Profile, error) {
	profiles := []Profile{}
	data, err := os.ReadFile(filepath.Join(s.dir, "profiles.json"))
	if errors.Is(err, os.ErrNotExist) {
		return profiles, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(data, &profiles); err != nil {
		return nil, err
	}
	for _, p := range profiles {
		if !validID.MatchString(p.ID) {
			return nil, fmt.Errorf("invalid profile index")
		}
	}
	return profiles, nil
}
func (s *Service) save(profiles []Profile) error {
	data, e := json.Marshal(profiles)
	if e != nil {
		return e
	}
	return atomicWrite(filepath.Join(s.dir, "profiles.json"), data)
}
func (s *Service) config(id string) string { return filepath.Join(s.dir, id+".conf") }
func (s *Service) desired() string {
	b, _ := os.ReadFile(filepath.Join(s.dir, "active"))
	id := strings.TrimSpace(string(b))
	if validID.MatchString(id) {
		return id
	}
	return ""
}
func existsInterface(id string) bool {
	_, e := os.Stat(filepath.Join("/sys/class/net", id))
	return e == nil
}
func command(timeout time.Duration, name string, args ...string) ([]byte, error) {
	return commandInput(timeout, nil, name, args...)
}
func commandInput(timeout time.Duration, input io.Reader, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = input
	cmd.Env = append(os.Environ(), "WG_ENDPOINT_RESOLUTION_RETRIES=2")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	// stderr may contain endpoint details or key validation errors; do not expose it.
	return cmd.Output()
}
func (s *Service) quick(action, id string) error {
	data, e := os.ReadFile(s.config(id))
	if e != nil {
		return fmt.Errorf("cannot read configuration")
	}
	profiles, e := s.profiles()
	if e != nil {
		return fmt.Errorf("cannot read WireGuard profiles")
	}
	routeAllowedIPs := false
	for _, p := range profiles {
		if p.ID == id {
			routeAllowedIPs = p.RouteAllowedIPs
			break
		}
	}
	normalized, e := profileConfig(string(data), routeAllowedIPs)
	if e != nil {
		return e
	}
	if action == "up" {
		// Migrate saved profiles before activation too. Keep the original Table
		// on down so wg-quick can remove rules from a previously active profile.
		if normalized != string(data) {
			if e = atomicWrite(s.config(id), []byte(normalized)); e != nil {
				return fmt.Errorf("cannot save WireGuard routing policy")
			}
			data = []byte(normalized)
		}
		for _, line := range strings.Split(string(data), "\n") {
			if (strings.HasPrefix(line, "Address = ") || (routeAllowedIPs && strings.HasPrefix(line, "AllowedIPs = "))) && strings.Contains(line, ":") {
				disabled, _ := os.ReadFile("/proc/sys/net/ipv6/conf/default/disable_ipv6")
				if strings.TrimSpace(string(disabled)) == "1" {
					return fmt.Errorf("WireGuard profile uses IPv6; enable IPv6 in Network settings first")
				}
			}
		}
		// Seed the existing LAN resolver on first use. Thereafter network/DHCP
		// updates replace nkos.base while wg-quick temporarily takes precedence.
		if strings.Contains(string(data), "DNS = ") {
			if _, err := os.Stat("/run/resolvconf/interfaces/nkos.base"); os.IsNotExist(err) {
				base, err := os.ReadFile("/etc/resolv.conf")
				if err != nil {
					return fmt.Errorf("WireGuard cannot read the existing DNS configuration")
				}
				if _, err = commandInput(5*time.Second, strings.NewReader(string(base)), "resolvconf", "-a", "nkos.base"); err != nil {
					return fmt.Errorf("WireGuard cannot preserve the existing DNS configuration")
				}
			}
		}
	}
	_, e = command(25*time.Second, "wg-quick", action, s.config(id))
	if e != nil {
		return fmt.Errorf("WireGuard %s failed; check the configuration, DNS and kernel support", action)
	}
	return nil
}
func (s *Service) restore() {
	// Network/DNS can become ready after the application starts. Limit retries;
	// never resurrect a profile the user disabled while waiting.
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(15 * time.Second)
		}
		s.mu.Lock()
		id := s.desired()
		if id == "" || existsInterface(id) {
			s.mu.Unlock()
			return
		}
		e := s.quick("up", id)
		if e == nil {
			delete(s.failures, id)
			s.mu.Unlock()
			return
		}
		s.failures[id] = e.Error()
		s.mu.Unlock()
	}
}
func (s *Service) GetStatus(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var rsp proto.Response
	profiles, e := s.profiles()
	if e != nil {
		rsp.ErrRsp(c, -1, "cannot read WireGuard profiles")
		return
	}
	statuses := make([]Status, 0, len(profiles))
	desired := s.desired()
	_, wgErr := exec.LookPath("wg")
	_, quickErr := exec.LookPath("wg-quick")
	for _, p := range profiles {
		st := Status{Profile: p, State: "off", Enabled: desired == p.ID, Error: s.failures[p.ID]}
		data, _ := os.ReadFile(s.config(p.ID))
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "Address = ") {
				if st.Address != "" {
					st.Address += ", "
				}
				st.Address += strings.TrimPrefix(line, "Address = ")
			}
		}
		if existsInterface(p.ID) {
			st.State = "waiting"
			b, err := command(2*time.Second, "wg", "show", p.ID, "latest-handshakes")
			if err != nil {
				st.State = "error"
				st.Error = "cannot read tunnel status"
			} else {
				for _, line := range strings.Split(string(b), "\n") {
					f := strings.Fields(line)
					if len(f) == 2 {
						n, _ := strconv.ParseInt(f[1], 10, 64)
						if n > st.LastHandshake {
							st.LastHandshake = n
						}
					}
				}
				if st.LastHandshake > 0 {
					st.State = "idle"
					if time.Now().Unix()-st.LastHandshake < 180 {
						st.State = "connected"
					}
				}
			}
			b, _ = command(2*time.Second, "wg", "show", p.ID, "transfer")
			for _, line := range strings.Split(string(b), "\n") {
				f := strings.Fields(line)
				if len(f) == 3 {
					rx, _ := strconv.ParseUint(f[1], 10, 64)
					tx, _ := strconv.ParseUint(f[2], 10, 64)
					st.Received += rx
					st.Sent += tx
				}
			}
		} else if st.Error != "" {
			st.State = "error"
		}
		statuses = append(statuses, st)
	}
	rsp.OkRspWithData(c, gin.H{"available": wgErr == nil && quickErr == nil, "profiles": statuses})
}
func (s *Service) Import(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var rsp proto.Response
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024*1024)
	form, e := c.MultipartForm()
	if e != nil {
		rsp.ErrRsp(c, -1, "upload .conf files (maximum 64 KiB each)")
		return
	}
	defer form.RemoveAll()
	files := form.File["files"]
	profiles, e := s.profiles()
	if e != nil || len(files) == 0 || len(profiles)+len(files) > 16 {
		rsp.ErrRsp(c, -1, "upload between 1 and 16 profiles in total")
		return
	}
	type pending struct {
		Profile
		data string
	}
	batch := []pending{}
	for _, f := range files {
		name := filepath.Base(f.Filename)
		if !strings.HasSuffix(strings.ToLower(name), ".conf") || len(name) > 128 {
			rsp.ErrRsp(c, -1, "use .conf filenames up to 128 bytes")
			return
		}
		file, err := f.Open()
		if err != nil {
			rsp.ErrRsp(c, -1, "cannot read upload")
			return
		}
		b, err := io.ReadAll(io.LimitReader(file, 64*1024+1))
		file.Close()
		if err != nil {
			rsp.ErrRsp(c, -1, "cannot read upload")
			return
		}
		cfg, err := Validate(string(b))
		if err != nil {
			rsp.ErrRsp(c, -1, err.Error())
			return
		}
		var random [5]byte
		if _, err = rand.Read(random[:]); err != nil {
			rsp.ErrRsp(c, -1, "cannot create profile ID")
			return
		}
		batch = append(batch, pending{Profile{ID: "nk" + hex.EncodeToString(random[:]), Name: strings.TrimSuffix(name, filepath.Ext(name))}, cfg})
	}
	committed := false
	defer func() {
		if !committed {
			for _, p := range batch {
				_ = os.Remove(s.config(p.ID))
			}
		}
	}()
	for _, p := range batch {
		if e = atomicWrite(s.config(p.ID), []byte(p.data)); e != nil {
			rsp.ErrRsp(c, -1, "cannot save profile")
			return
		}
		profiles = append(profiles, p.Profile)
	}
	if e = s.save(profiles); e != nil {
		rsp.ErrRsp(c, -1, "cannot save profile index")
		return
	}
	committed = true
	rsp.OkRsp(c)
}
func (s *Service) Change(c *gin.Context) {
	if !s.mu.TryLock() {
		var rsp proto.Response
		rsp.ErrRsp(c, -1, "another VPN operation is in progress")
		return
	}
	defer s.mu.Unlock()
	var rsp proto.Response
	var req struct {
		ID              string  `json:"id"`
		Action          string  `json:"action"`
		Name            *string `json:"name"`
		RouteAllowedIPs *bool   `json:"routeAllowedIPs"`
	}
	if c.ShouldBindJSON(&req) != nil || !validID.MatchString(req.ID) {
		rsp.ErrRsp(c, -1, "invalid profile")
		return
	}
	profiles, e := s.profiles()
	if e != nil {
		rsp.ErrRsp(c, -1, "cannot read profiles")
		return
	}
	idx := -1
	for i, p := range profiles {
		if p.ID == req.ID {
			idx = i
		}
	}
	if idx < 0 {
		rsp.ErrRsp(c, -1, "profile not found")
		return
	}
	id := req.ID
	switch req.Action {
	case "rename":
		if req.Name == nil {
			rsp.ErrRsp(c, -1, "profile name is required")
			return
		}
		name := strings.TrimSpace(*req.Name)
		if name == "" || utf8.RuneCountInString(name) > 128 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
			rsp.ErrRsp(c, -1, "use a profile name of 1–128 characters without control characters")
			return
		}
		// A display name is metadata only. Keep the interface ID, config file,
		// boot restore marker and current tunnel/error state unchanged.
		profiles[idx].Name = name
		if err := s.save(profiles); err != nil {
			rsp.ErrRsp(c, -1, "cannot rename WireGuard profile")
			return
		}
		rsp.OkRsp(c)
		return
	case "routing":
		if req.RouteAllowedIPs == nil {
			rsp.ErrRsp(c, -1, "routeAllowedIPs is required")
			return
		}
		if existsInterface(id) || s.desired() == id {
			rsp.ErrRsp(c, -1, "disable this profile before changing routing")
			return
		}
		profiles[idx].RouteAllowedIPs = *req.RouteAllowedIPs
		e = s.save(profiles)
	case "up":
		for _, p := range profiles {
			if p.ID != id && (existsInterface(p.ID) || s.desired() == p.ID) {
				rsp.ErrRsp(c, -1, "disable the active profile before switching")
				return
			}
		}
		if !existsInterface(id) {
			e = s.quick("up", id)
		}
		if e == nil {
			e = atomicWrite(filepath.Join(s.dir, "active"), []byte(id+"\n"))
			if e != nil {
				_ = s.quick("down", id)
			}
		}
	case "down", "delete":
		// Disable boot restore first; failed teardown remains visible and retryable.
		if s.desired() == id {
			e = os.Remove(filepath.Join(s.dir, "active"))
			if errors.Is(e, os.ErrNotExist) {
				e = nil
			}
		}
		if e == nil && existsInterface(id) {
			e = s.quick("down", id)
		}
		if e == nil && req.Action == "delete" {
			next := append(profiles[:idx:idx], profiles[idx+1:]...)
			e = s.save(next)
			if e == nil {
				e = os.Remove(s.config(id))
			}
		}
	default:
		rsp.ErrRsp(c, -1, "invalid action")
		return
	}
	if e != nil {
		message := "cannot save WireGuard state"
		if strings.HasPrefix(e.Error(), "WireGuard ") {
			message = e.Error()
		}
		s.failures[id] = message
		rsp.ErrRsp(c, -1, message)
		return
	}
	delete(s.failures, id)
	rsp.OkRsp(c)
}
