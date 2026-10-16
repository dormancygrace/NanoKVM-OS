package storage

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/hid"
	"NanoKVM-Server/usbinternet"
)

// Network boot: the official iPXE CD image goes on the virtual disk, the
// NanoKVM DHCP server on USB network hands iPXE (and only iPXE) the address of
// a one-screen script on usb0, and that script loads the netboot.xyz menus of
// the selected release over USB internet sharing. The host boots from the
// virtual CD; nothing but the iPXE image is stored on the NanoKVM.
//
// It is a runtime state, like an image mount: a NanoKVM restart ends it, and
// the next server start undoes what it changed.

const netbootPort = 8070

var (
	usbNetworkLink  = "/sys/kernel/config/usb_gadget/g0/configs/c.1/ncm.usb0"
	usbInternetRun  = "/etc/init.d/S30usbnet"
	usbInternetTool = "/usr/sbin/nkos-usb-internet"
	netbootRuntime  = usbinternet.DefaultRuntime
	// applyUSBNetwork reconciles USB networking with the flags just written.
	applyUSBNetwork = func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := exec.CommandContext(ctx, "sh", usbInternetRun, "start").Run(); err != nil {
			return fmt.Errorf("apply USB network: %w", err)
		}
		return nil
	}
	mountNetbootImage = mountLocalImage
)

type netbootSession struct {
	image      string
	secureBoot bool
	netbootxyz atomic.Value // string: release tag of the menus
	address    string       // usb0 address the menu listens on
	server     *http.Server
	fetches    atomic.Int64
	lastFetch  atomic.Int64 // unix seconds
	stop       chan struct{}
}

// netbootScript is the only thing the menu listener serves. It sets what the
// netboot.xyz Secure Boot autoexec.ipxe of the release sets, then loads that
// release's menus. tag is validated by netbootxyzTag.
func netbootScript(tag string) string {
	return "#!ipxe\n" +
		"# NanoKVM network boot: the netboot.xyz " + tag + " menus.\n" +
		"set site_name netboot.xyz\n" +
		"set boot_domain boot.netboot.xyz/" + tag + "\n" +
		"set ipxe_version ${version}\n" +
		"set version " + tag + "\n" +
		"chain --autofree https://${boot_domain}/menu.ipxe || chain --autofree http://${boot_domain}/menu.ipxe || goto failed\n" +
		":failed\n" +
		"echo netboot.xyz is not reachable through the NanoKVM. Check its internet connection.\n" +
		"prompt --timeout 30000 Press any key for the iPXE shell... ||\n" +
		"shell\n"
}

func (n *netbootSession) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/boot.ipxe", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		n.fetches.Add(1)
		n.lastFetch.Store(time.Now().Unix())
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(netbootScript(n.netbootxyz.Load().(string))))
	})
	return mux
}

// listen serves the script on address:netbootPort only, never on all addresses.
func (n *netbootSession) listen(address string) error {
	l, err := net.Listen("tcp", net.JoinHostPort(address, strconv.Itoa(netbootPort)))
	if err != nil {
		return fmt.Errorf("network boot menu: %w", err)
	}
	server := &http.Server{Handler: n.handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		if err := server.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Warnf("network boot menu: %v", err)
		}
	}()
	if n.server != nil {
		_ = n.server.Close()
	}
	n.server, n.address = server, address
	return nil
}

func (n *netbootSession) close() {
	close(n.stop)
	if n.server != nil {
		_ = n.server.Close()
	}
}

// follow moves the listener when USB networking picks another subnet (it
// does so on a conflict with another network).
func (s *Service) follow(n *netbootSession) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-n.stop:
			return
		case <-ticker.C:
		}
		address := usbAddress(netbootRuntime().ReadStatus())
		s.mu.Lock()
		if s.netboot == n && address != "" && address != n.address {
			if err := n.listen(address); err != nil {
				log.Warnf("network boot: %v", err)
			} else {
				log.Infof("network boot menu moved to %s", address)
			}
		}
		s.mu.Unlock()
	}
}

func usbAddress(st usbinternet.Status) string {
	p, err := netip.ParsePrefix(st.Address)
	if err != nil {
		return ""
	}
	return p.Addr().String()
}

func usbNetworkActive() bool {
	info, err := os.Lstat(usbNetworkLink)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

// setSharing writes or removes the persistent USB internet flag.
func setSharing(r usbinternet.Runtime, enabled bool) error {
	path := filepath.Join(r.Boot, "usb.internet")
	if !enabled {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return os.WriteFile(path, nil, 0o644)
}

func applyLocked() error {
	h := hid.GetHid()
	h.Lock()
	defer h.Unlock()
	return applyUSBNetwork()
}

// undoNetboot withdraws the menu port and, if network boot turned it on, USB
// internet sharing. It is the cleanup of a disable, a failed enable and a
// server start after network boot was left on.
func undoNetboot(st *netbootState) error {
	r := netbootRuntime()
	errs := []error{r.SetNetbootPort(0)}
	if st.SharingByNetboot {
		errs = append(errs, setSharing(r, false))
	}
	errs = append(errs, applyLocked())
	err := errors.Join(errs...)
	if err == nil && st.SharingByNetboot {
		st.SharingByNetboot = false
		err = saveNetbootState(*st)
	}
	return err
}

type netbootStatusRsp struct {
	Enabled      bool               `json:"enabled"`
	SecureBoot   bool               `json:"secureBoot"`
	IPXE         string             `json:"ipxe"`
	Netbootxyz   string             `json:"netbootxyz"`
	CheckedAt    string             `json:"checkedAt"`
	Menu         string             `json:"menu,omitempty"`
	MenuFetches  int64              `json:"menuFetches"`
	LastFetch    int64              `json:"lastFetch,omitempty"`
	USBNetwork   bool               `json:"usbNetwork"`
	VirtualDisk  bool               `json:"virtualDisk"`
	USBInternet  usbinternet.Status `json:"usbInternet"`
	BrowserMedia bool               `json:"browserMedia"`
}

func (s *Service) netbootStatus() (netbootStatusRsp, error) {
	st, err := loadNetbootState()
	s.mu.Lock()
	defer s.mu.Unlock()
	rsp := netbootStatusRsp{
		SecureBoot:   st.SecureBoot,
		IPXE:         st.IPXE,
		Netbootxyz:   st.Netbootxyz,
		CheckedAt:    st.CheckedAt,
		USBNetwork:   usbNetworkActive(),
		VirtualDisk:  requireActiveUSBStorage() == nil,
		USBInternet:  netbootRuntime().ReadStatus(),
		BrowserMedia: s.remote != nil,
	}
	if n := s.netboot; n != nil {
		rsp.Enabled, rsp.SecureBoot = true, n.secureBoot
		rsp.Menu = "http://" + net.JoinHostPort(n.address, strconv.Itoa(netbootPort)) + "/boot.ipxe"
		rsp.MenuFetches, rsp.LastFetch = n.fetches.Load(), n.lastFetch.Load()
	}
	return rsp, err
}

func (s *Service) GetNetboot(c *gin.Context) {
	var rsp proto.Response
	status, err := s.netbootStatus()
	if err != nil {
		log.Warnf("network boot: %v", err)
	}
	rsp.OkRspWithData(c, status)
}

// UpdateNetboot installs the latest iPXE images and selects the latest
// netboot.xyz release. While network boot is on, the new image replaces the
// mounted one.
func (s *Service) UpdateNetboot(c *gin.Context) {
	var rsp proto.Response
	s.netbootOp.Lock()
	defer s.netbootOp.Unlock()
	st, err := loadNetbootState()
	if err != nil {
		rsp.ErrRsp(c, -2, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Minute)
	defer cancel()
	next, err := updateNetbootReleases(ctx, st)
	if err != nil {
		log.Errorf("network boot update: %v", err)
		rsp.ErrRsp(c, -3, err.Error())
		return
	}
	s.mu.Lock()
	n := s.netboot
	if n != nil {
		n.netbootxyz.Store(next.Netbootxyz)
		if image := ipxeImagePath(next.IPXE, imageFor(n.secureBoot)); image != n.image {
			if err = mountNetbootImage(image, true, false); err == nil {
				n.image = image
			}
		}
	}
	s.mu.Unlock()
	if err != nil {
		rsp.ErrRsp(c, -4, "Updated, but the new iPXE image could not be mounted: "+err.Error())
		return
	}
	removeOldIPXE(next.IPXE)
	status, _ := s.netbootStatus()
	rsp.OkRspWithData(c, status)
}

func imageFor(secureBoot bool) string {
	if secureBoot {
		return ipxeSBImage
	}
	return ipxeImage
}

func (s *Service) EnableNetboot(c *gin.Context) {
	var req proto.EnableNetbootReq
	var rsp proto.Response
	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}
	s.netbootOp.Lock()
	defer s.netbootOp.Unlock()
	if code, err := s.enableNetboot(c.Request.Context(), req.SecureBoot); err != nil {
		log.Errorf("network boot: %v", err)
		rsp.ErrRsp(c, code, err.Error())
		return
	}
	status, _ := s.netbootStatus()
	rsp.OkRspWithData(c, status)
}

func (s *Service) enableNetboot(ctx context.Context, secureBoot bool) (int, error) {
	s.mu.Lock()
	busy, on := s.remote != nil, s.netboot != nil
	s.mu.Unlock()
	if busy {
		return -5, errors.New("Disconnect browser media before turning on network boot")
	}
	if on {
		if code, err := s.disableNetboot(); err != nil {
			return code, err
		}
	}
	if !usbNetworkActive() {
		return -6, errors.New("Turn on USB network in the USB composition first")
	}
	if err := requireActiveUSBStorage(); err != nil {
		return -6, errors.New("Turn on the virtual disk in the USB composition first")
	}
	if _, err := os.Stat(usbInternetTool); err != nil {
		return -4, errors.New("Network boot requires the updated Alpine base package")
	}

	st, err := loadNetbootState()
	if err != nil {
		return -2, err
	}
	if st.IPXE == "" || st.Netbootxyz == "" {
		dl, cancel := context.WithTimeout(ctx, 10*time.Minute)
		st, err = updateNetbootReleases(dl, st)
		cancel()
		if err != nil {
			return -3, fmt.Errorf("download network boot files: %w", err)
		}
		removeOldIPXE(st.IPXE)
	}
	name := imageFor(secureBoot)
	image := ipxeImagePath(st.IPXE, name)
	if err = verifyFile(image, st.IPXEFiles[name]); err != nil {
		return -2, err
	}

	r := netbootRuntime()
	if !r.Enabled() {
		st.SharingByNetboot = true
		if err = saveNetbootState(st); err != nil {
			return -2, err
		}
		if err = setSharing(r, true); err != nil {
			_ = undoNetboot(&st)
			return -2, err
		}
	}
	fail := func(code int, err error) (int, error) {
		if undo := undoNetboot(&st); undo != nil {
			err = fmt.Errorf("%w; undo: %v", err, undo)
		}
		return code, err
	}
	if err = r.SetNetbootPort(netbootPort); err != nil {
		return fail(-2, err)
	}
	if err = applyLocked(); err != nil {
		return fail(-2, err)
	}
	address := usbAddress(r.ReadStatus())
	if address == "" {
		return fail(-6, errors.New("USB network has no address; check the USB network status"))
	}

	n := &netbootSession{image: image, secureBoot: secureBoot, stop: make(chan struct{})}
	n.netbootxyz.Store(st.Netbootxyz)
	s.mu.Lock()
	if s.remote != nil {
		s.mu.Unlock()
		return fail(-5, errors.New("Disconnect browser media before turning on network boot"))
	}
	if err = n.listen(address); err != nil {
		s.mu.Unlock()
		return fail(-2, err)
	}
	if err = mountNetbootImage(image, true, false); err != nil {
		// The mount can fail after writing the LUN (for example while
		// rebinding USB). Do not leave that partial mount behind.
		err = errors.Join(err, ejectNetbootImage())
		s.mu.Unlock()
		n.close()
		return fail(-2, err)
	}
	s.netboot = n
	s.mu.Unlock()
	go s.follow(n)

	st.SecureBoot = secureBoot
	if err = saveNetbootState(st); err != nil {
		log.Warnf("network boot: %v", err)
	}
	log.Infof("network boot on: %s, netboot.xyz %s, menu on %s", filepath.Base(image), st.Netbootxyz, address)
	return 0, nil
}

func (s *Service) DisableNetboot(c *gin.Context) {
	var rsp proto.Response
	s.netbootOp.Lock()
	defer s.netbootOp.Unlock()
	if code, err := s.disableNetboot(); err != nil {
		log.Errorf("network boot: %v", err)
		rsp.ErrRsp(c, code, err.Error())
		return
	}
	status, _ := s.netbootStatus()
	rsp.OkRspWithData(c, status)
}

// ejectNetbootImage also works when the storage function has been unlinked
// or the gadget is unbound. Ejecting does not need to rebind the whole gadget,
// which would interrupt the host's USB network connection.
func ejectNetbootImage() error {
	h := hid.GetHid()
	h.Lock()
	defer h.Unlock()
	current, err := os.ReadFile(mountDevice)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.HasPrefix(strings.TrimSpace(string(current)), netbootDir+string(filepath.Separator)) {
		return nil
	}
	return ejectLocalImage(true, os.WriteFile)
}

func (s *Service) disableNetboot() (int, error) {
	st, err := loadNetbootState()
	if err != nil {
		return -2, err
	}
	s.mu.Lock()
	// Keep the session and cleanup markers until cleanup succeeds. A
	// failed disable must remain retryable, including after a server restart.
	err = ejectNetbootImage()
	s.mu.Unlock()
	if err != nil {
		return -2, err
	}
	if err = undoNetboot(&st); err != nil {
		return -2, err
	}
	s.mu.Lock()
	if n := s.netboot; n != nil {
		n.close()
		s.netboot = nil
	}
	s.mu.Unlock()
	return 0, nil
}

// CleanupNetboot runs once at server start: a previous server process may
// have left network boot on (the menu port, sharing it enabled, its image on
// the virtual disk). Without a session nothing serves the menu any more.
func (s *Service) CleanupNetboot() {
	s.netbootOp.Lock()
	defer s.netbootOp.Unlock()
	st, err := loadNetbootState()
	if err != nil {
		log.Warnf("network boot: %v", err)
		return
	}
	s.mu.Lock()
	err = ejectNetbootImage()
	s.mu.Unlock()
	if err != nil {
		log.Warnf("network boot: eject the iPXE image: %v", err)
		return
	}
	r := netbootRuntime()
	if r.NetbootPort() == 0 && !st.SharingByNetboot {
		return
	}
	if err := undoNetboot(&st); err != nil {
		log.Warnf("network boot: undo after restart: %v", err)
	}
}
