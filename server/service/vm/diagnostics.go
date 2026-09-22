package vm

// This file deliberately collects a small allow-list of support data. It is
// not a general system inspector: diagnostics must remain safe to download in
// an issue without copying configuration, credentials, network addresses, or
// video data.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"NanoKVM-Server/common"
	"NanoKVM-Server/osupdate"
	"NanoKVM-Server/proto"
	"NanoKVM-Server/utils"
	"github.com/gin-gonic/gin"
)

const (
	diagnosticsCacheTTL = 20 * time.Second
	diagnosticsTimeout  = 2 * time.Second
	diagnosticsBudget   = 8 * time.Second
	diagnosticsOutput   = 64 * 1024
)

// State is intentionally explicit: a missing optional component is not an
// error, and a failed probe is never presented as a stopped service.
type DiagnosticItem struct {
	State    string `json:"state"` // ok, running, stopped, absent, unavailable, error
	Detail   string `json:"detail,omitempty"`
	Optional bool   `json:"optional,omitempty"`
}

type DiagnosticService struct {
	Name    string `json:"name"`
	Desired string `json:"desired,omitempty"`
	DiagnosticItem
}

type DiagnosticHookChain struct {
	Label  string `json:"label"`
	Hook   string `json:"hook"`
	Policy string `json:"policy,omitempty"`
}

type DiagnosticPackage struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	State   string `json:"state"`
}
type reportService struct {
	Name     string `json:"name"`
	State    string `json:"state"`
	Optional bool   `json:"optional,omitempty"`
	Desired  string `json:"desired,omitempty"`
}

type DiagnosticsSnapshot struct {
	CollectedAt int64 `json:"collectedAt"`
	Versions    struct {
		Application string              `json:"application,omitempty"`
		Image       string              `json:"image,omitempty"`
		Alpine      string              `json:"alpine,omitempty"`
		Kernel      string              `json:"kernel,omitempty"`
		SystemBase  string              `json:"systemBase,omitempty"`
		Modules     DiagnosticItem      `json:"modules"`
		Boot        DiagnosticItem      `json:"boot"`
		Packages    []DiagnosticPackage `json:"packages"`
	} `json:"versions"`
	Services []DiagnosticService `json:"services"`
	Video    struct {
		Enabled     bool   `json:"enabled"`
		Signal      bool   `json:"signal"`
		Profile     string `json:"profile,omitempty"`
		Input       string `json:"input,omitempty"`
		Output      string `json:"output,omitempty"`
		MeasuredFPS string `json:"measuredFps,omitempty"`
		EDIDState   string `json:"edidState"`
		State       string `json:"state"`
	} `json:"video"`
	USB struct {
		Selected []string       `json:"selected"`
		Binding  DiagnosticItem `json:"binding"`
	} `json:"usb"`
	APK struct {
		State               string `json:"state"`
		UpdateState         string `json:"updateState"`
		ErrorCategory       string `json:"errorCategory,omitempty"`
		UpdateErrorCategory string `json:"updateErrorCategory,omitempty"`
	} `json:"apk"`
	Firewall struct {
		State      string                `json:"state"`
		HookChains []DiagnosticHookChain `json:"hookChains"`
		Detail     string                `json:"detail,omitempty"`
	} `json:"firewall"`
}

var (
	diagnosticInitDir    = "/etc/init.d"
	diagnosticModulesDir = "/lib/modules"
	diagnosticBootDir    = "/boot"
	diagnosticGadgetDir  = "/sys/kernel/config/usb_gadget/g0"
	diagnosticAPKDB      = "/lib/apk/db/installed"
	diagnosticOpenVPNRun = "/run/nkos-openvpn"
	diagnosticProcDir    = "/proc"
	diagnosticReadFile   = os.ReadFile
	diagnosticReadDir    = os.ReadDir
	diagnosticStat       = os.Stat
	diagnosticLookPath   = exec.LookPath
	diagnosticRun        = runDiagnosticCommand
	diagnosticOpenVPN    = exec.LookPath
	diagnosticNow        = time.Now
)

type diagnosticsCache struct {
	mu       sync.Mutex
	snapshot DiagnosticsSnapshot
	at       time.Time
	inflight chan struct{}
	err      error
}

var cachedDiagnostics diagnosticsCache

func (s *Service) GetDiagnostics(c *gin.Context) {
	snapshot, err := readDiagnostics(c.Request.Context())
	var rsp proto.Response
	if err != nil {
		rsp.ErrRsp(c, -1, err.Error())
		return
	}
	rsp.OkRspWithData(c, snapshot)
}

// DownloadDiagnosticsReport returns an issue-ready report. It is a JSON
// attachment rather than a server-side upload: the user remains in control of
// where it goes.
func (s *Service) DownloadDiagnosticsReport(c *gin.Context) {
	snapshot, err := readDiagnostics(c.Request.Context())
	if err != nil {
		c.JSON(500, gin.H{"code": -1, "msg": err.Error()})
		return
	}
	report := diagnosticsReport(snapshot)
	c.Header("Content-Disposition", "attachment; filename=nanokvm-diagnostics.json")
	c.JSON(200, report)
}

// diagnosticsReport is intentionally narrower than the UI snapshot. Never add
// command output, operation messages, network data, or user supplied names.
func diagnosticsReport(snapshot DiagnosticsSnapshot) any {
	return struct {
		Schema      string          `json:"schema"`
		CollectedAt int64           `json:"collectedAt"`
		Privacy     string          `json:"privacy"`
		Versions    any             `json:"versions"`
		Services    []reportService `json:"services"`
		Video       any             `json:"video"`
		USB         any             `json:"usb"`
		APK         any             `json:"apk"`
		Firewall    any             `json:"firewall"`
	}{
		Schema: "nanokvm-diagnostics/v1", CollectedAt: snapshot.CollectedAt,
		Privacy: "Allow-listed status only; no credentials, configuration contents, network addresses, identifiers, screen, or video data.",
		Versions: struct {
			Application, Image, Alpine, Kernel, SystemBase string
			Modules, Boot                                  string
			Packages                                       []DiagnosticPackage
		}{safeScalar(snapshot.Versions.Application), safeScalar(snapshot.Versions.Image), safeScalar(snapshot.Versions.Alpine), safeScalar(snapshot.Versions.Kernel), safeSystemBase(snapshot.Versions.SystemBase), safeState(snapshot.Versions.Modules.State), safeState(snapshot.Versions.Boot.State), reportPackages(snapshot.Versions.Packages)},
		Services: reportServices(snapshot.Services),
		Video:    struct{ State, EDIDState, Profile, Input, Output, MeasuredFPS string }{safeState(snapshot.Video.State), safeState(snapshot.Video.EDIDState), safeScalar(snapshot.Video.Profile), safeDimension(snapshot.Video.Input), safeDimension(snapshot.Video.Output), safeScalar(snapshot.Video.MeasuredFPS)},
		USB: struct {
			Selected []string
			Binding  string
		}{reportUSBSelected(snapshot.USB.Selected), safeState(snapshot.USB.Binding.State)},
		APK: struct{ State, UpdateState, ErrorCategory, UpdateErrorCategory string }{safeState(snapshot.APK.State), safeState(snapshot.APK.UpdateState), safeState(snapshot.APK.ErrorCategory), safeState(snapshot.APK.UpdateErrorCategory)},
		Firewall: struct {
			State      string
			HookChains []DiagnosticHookChain
		}{safeState(snapshot.Firewall.State), reportHookChains(snapshot.Firewall.HookChains)},
	}
}

var reportServiceNames = map[string]bool{"nanokvm-storage": true, "nanokvm-modules": true, "nanokvm-board": true, "nanokvm-network": true, "nanokvm-app": true, "nanokvm-usb": true, "nanokvm-ssh": true, "chronyd": true, "dnsmasq": true, "tailscaled": true, "netbird": true, "openvpn client": true}
var reportUSBNames = map[string]bool{"keyboard": true, "relative mouse": true, "absolute mouse": true, "network": true, "disk": true, "serial": true, "audio": true}

func reportServices(items []DiagnosticService) []reportService {
	result := []reportService{}
	for _, item := range items {
		if reportServiceNames[item.Name] {
			result = append(result, reportService{Name: item.Name, State: safeState(item.State), Optional: item.Optional, Desired: safeDesired(item.Desired)})
		}
	}
	return result
}
func safeDesired(value string) string {
	switch value {
	case "enabled", "disabled", "on-demand", "optional":
		return value
	}
	return ""
}
func reportUSBSelected(items []string) []string {
	result := []string{}
	for _, item := range items {
		if reportUSBNames[item] {
			result = append(result, item)
		}
	}
	return result
}
func reportPackages(items []DiagnosticPackage) []DiagnosticPackage {
	result := []DiagnosticPackage{}
	allowed := map[string]bool{}
	for _, name := range diagnosticPackages {
		allowed[name] = true
	}
	for _, item := range items {
		if allowed[item.Name] {
			result = append(result, DiagnosticPackage{Name: item.Name, Version: safeScalar(item.Version), State: safeState(item.State)})
		}
	}
	return result
}
func reportHookChains(items []DiagnosticHookChain) []DiagnosticHookChain {
	result := []DiagnosticHookChain{}
	for i, item := range items {
		result = append(result, DiagnosticHookChain{Label: "chain-" + strconv.Itoa(i+1), Hook: safeNFTHook(item.Hook), Policy: safeNFTPolicy(item.Policy)})
	}
	return result
}

func readDiagnostics(ctx context.Context) (DiagnosticsSnapshot, error) {
	now := diagnosticNow()
	cachedDiagnostics.mu.Lock()
	if !cachedDiagnostics.at.IsZero() && now.Sub(cachedDiagnostics.at) < diagnosticsCacheTTL {
		snapshot := cachedDiagnostics.snapshot
		cachedDiagnostics.mu.Unlock()
		return snapshot, nil
	}
	if inflight := cachedDiagnostics.inflight; inflight != nil {
		cachedDiagnostics.mu.Unlock()
		select {
		case <-inflight:
			cachedDiagnostics.mu.Lock()
			snapshot, err := cachedDiagnostics.snapshot, cachedDiagnostics.err
			cachedDiagnostics.mu.Unlock()
			return snapshot, err
		case <-ctx.Done():
			return DiagnosticsSnapshot{}, ctx.Err()
		}
	}
	inflight := make(chan struct{})
	cachedDiagnostics.inflight = inflight
	cachedDiagnostics.mu.Unlock()

	snapshot, err := collectDiagnostics(ctx)
	cachedDiagnostics.mu.Lock()
	if err == nil {
		cachedDiagnostics.snapshot, cachedDiagnostics.at = snapshot, now
	}
	cachedDiagnostics.err, cachedDiagnostics.inflight = err, nil
	close(inflight)
	cachedDiagnostics.mu.Unlock()
	return snapshot, err
}

func collectDiagnostics(ctx context.Context) (DiagnosticsSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, diagnosticsBudget)
	defer cancel()
	result := DiagnosticsSnapshot{CollectedAt: diagnosticNow().UnixMilli(), Services: []DiagnosticService{}}
	result.Versions.Application = safeScalar(getApplicationVersion())
	result.Versions.Image = safeScalar(getImageVersion())
	result.Versions.Alpine = safeScalar(diagnosticText("/etc/alpine-release"))
	result.Versions.Kernel = safeScalar(diagnosticText("/proc/sys/kernel/osrelease"))
	result.Versions.SystemBase = safeScalar(diagnosticText("/etc/nkos-system-base"))
	bootTarget := diagnosticText(filepath.Join(diagnosticBootDir, "kernel.release"))
	pending := osupdate.GetResult().Reboot || rebootRequired()
	result.Versions.Modules = kernelModules(result.Versions.Kernel, bootTarget, pending)
	result.Versions.Boot = bootStatus(result.Versions.Kernel, pending)
	result.Versions.Packages = installedPackageVersions()
	result.Services = collectServiceStatus(ctx)

	result.Video.Enabled = !utils.IsHdmiDisabled()
	result.Video.Signal = result.Video.Enabled && common.GetKvmVision().HasHDMISignal()
	result.Video.Profile = monitorProfile()
	result.Video.Input = dimensions(videoValue(common.ReadVideoValue("/run/nanokvm/width")), videoValue(common.ReadVideoValue("/run/nanokvm/height")))
	result.Video.Output = dimensions(videoValue(common.ReadVideoValue("/run/nanokvm/stream_width")), videoValue(common.ReadVideoValue("/run/nanokvm/stream_height")))
	result.Video.MeasuredFPS = videoValue(common.ReadVideoValue("/run/nanokvm/now_fps"))
	result.Video.EDIDState = "saved-profile"
	if !common.MonitorProfileSupported() {
		result.Video.EDIDState = "unavailable"
	} else if common.MonitorPowerCyclePending() {
		result.Video.EDIDState = "power-cycle-pending"
	}
	result.Video.State = "ok"
	if !result.Video.Enabled {
		result.Video.State = "disabled"
	} else if !result.Video.Signal {
		result.Video.State = "no-signal"
	}

	result.USB.Selected, result.USB.Binding = usbStatus()
	apk := osupdate.GetAPKStatus()
	result.APK.State = safeState(apk.State)
	if apk.State == "failed" {
		result.APK.State = "error"
		result.APK.ErrorCategory = "operation-failed"
	}
	update := osupdate.GetResult()
	result.APK.UpdateState = safeState(update.State)
	if update.State == "failed" {
		result.APK.UpdateState = "error"
		result.APK.UpdateErrorCategory = "operation-failed"
	}
	result.Firewall.State, result.Firewall.HookChains, result.Firewall.Detail = firewallStatus(ctx)
	return result, nil
}

func diagnosticText(path string) string {
	b, err := diagnosticReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

var safeDiagnosticValue = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,128}$`)
var safeDiagnosticDimension = regexp.MustCompile(`^[0-9]{1,5} × [0-9]{1,5}$`)

func safeScalar(value string) string {
	value = strings.TrimSpace(value)
	if !safeDiagnosticValue.MatchString(value) {
		return ""
	}
	return value
}

func safeDimension(value string) string {
	if safeDiagnosticDimension.MatchString(value) {
		return value
	}
	return ""
}

var safeSystemBaseValue = regexp.MustCompile(`^[a-f0-9]{64}$`)

func safeSystemBase(value string) string {
	if safeSystemBaseValue.MatchString(value) {
		return value
	}
	return ""
}

func safeState(value string) string {
	switch value {
	case "ok", "running", "stopped", "absent", "unavailable", "error", "disabled", "configured", "no-signal", "checking", "installing", "ready", "up-to-date", "installed", "idle", "operation-failed", "saved-profile", "power-cycle-pending", "pending-reboot":
		return value
	default:
		return "unavailable"
	}
}

func rebootRequired() bool {
	_, err := diagnosticStat("/run/reboot-required")
	return err == nil
}

var diagnosticPackages = []string{"nanokvm-app", "nanokvm-base", "nanokvm-release", "nanokvm-kernel-sg2002", "nanokvm-kmod-sg2002", "nanokvm-firmware-sg2002"}

func installedPackageVersions() []DiagnosticPackage {
	result := make([]DiagnosticPackage, 0, len(diagnosticPackages))
	versions := map[string]string{}
	b, err := diagnosticReadFile(diagnosticAPKDB)
	if err == nil {
		name := ""
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "P:") {
				name = strings.TrimPrefix(line, "P:")
			}
			if strings.HasPrefix(line, "V:") && name != "" {
				versions[name] = safeScalar(strings.TrimPrefix(line, "V:"))
			}
			if line == "" {
				name = ""
			}
		}
	}
	for _, name := range diagnosticPackages {
		item := DiagnosticPackage{Name: name, State: "absent"}
		if err != nil {
			item.State = "unavailable"
		} else if version := versions[name]; version != "" {
			item.State, item.Version = "installed", version
		}
		result = append(result, item)
	}
	return result
}

func kernelModules(kernel, target string, pendingReboot bool) DiagnosticItem {
	if kernel == "" {
		return DiagnosticItem{State: "unavailable", Detail: "kernel release unavailable"}
	}
	if modulesMetadata(kernel) {
		return DiagnosticItem{State: "ok", Detail: "module metadata matches running kernel; ABI not verified"}
	}
	if pendingReboot && target != "" && target != kernel && modulesMetadata(target) {
		return DiagnosticItem{State: "pending-reboot", Detail: "new kernel modules installed; reboot is pending"}
	}
	if _, err := diagnosticStat(filepath.Join(diagnosticModulesDir, kernel)); errors.Is(err, os.ErrNotExist) {
		return DiagnosticItem{State: "error", Detail: "no installed modules for running kernel"}
	}
	return DiagnosticItem{State: "unavailable", Detail: "matching module directory lacks usable metadata"}
}
func modulesMetadata(release string) bool {
	info, err := diagnosticStat(filepath.Join(diagnosticModulesDir, release, "modules.dep"))
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

func bootStatus(kernel string, pendingReboot bool) DiagnosticItem {
	if _, err := diagnosticStat(diagnosticBootDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DiagnosticItem{State: "error", Detail: "boot partition unavailable"}
		}
		return DiagnosticItem{State: "unavailable", Detail: "cannot inspect boot partition"}
	}
	if _, err := diagnosticStat(filepath.Join(diagnosticBootDir, "boot.sd")); err != nil {
		return DiagnosticItem{State: "error", Detail: "boot FIT is unavailable"}
	}
	bootKernel := diagnosticText(filepath.Join(diagnosticBootDir, "kernel.release"))
	if bootKernel == "" {
		return DiagnosticItem{State: "unavailable", Detail: "boot release marker is unavailable"}
	}
	if kernel != "" && bootKernel != kernel {
		if pendingReboot {
			return DiagnosticItem{State: "pending-reboot", Detail: "boot marker differs; reboot is pending"}
		}
		return DiagnosticItem{State: "error", Detail: "boot kernel does not match running kernel"}
	}
	return DiagnosticItem{State: "ok", Detail: "boot marker matches running kernel; FIT ABI not verified"}
}

func serviceStatus(ctx context.Context, name string, optional bool) DiagnosticItem {
	item := DiagnosticItem{Optional: optional}
	if _, err := diagnosticStat(filepath.Join(diagnosticInitDir, name)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			item.State, item.Detail = "absent", "service is not installed"
		} else {
			item.State, item.Detail = "unavailable", "cannot inspect service definition"
		}
		return item
	}
	if _, err := diagnosticLookPath("rc-service"); err != nil {
		item.State, item.Detail = "unavailable", "OpenRC status command is unavailable"
		return item
	}
	probe, cancel := context.WithTimeout(ctx, diagnosticsTimeout)
	defer cancel()
	_, err := diagnosticRun(probe, diagnosticsOutput, "rc-service", name, "status")
	if errors.Is(probe.Err(), context.DeadlineExceeded) {
		item.State, item.Detail = "error", "OpenRC status timed out"
		return item
	}
	if err == nil {
		item.State, item.Detail = "running", "reported running by OpenRC"
		return item
	}
	// OpenRC reserves exit status 3 for a stopped service. Do not rely on
	// pidof: supervised daemons and ones with multiple processes need OpenRC.
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 3 {
		item.State, item.Detail = "stopped", "installed but not running"
		return item
	}
	item.State, item.Detail = "error", "OpenRC status command failed"
	return item
}

func collectServiceStatus(ctx context.Context) []DiagnosticService {
	type spec struct {
		name, desired, disabledMarker string
		optional                      bool
	}
	specs := []spec{
		{"nanokvm-storage", "enabled", "", false}, {"nanokvm-modules", "enabled", "", false},
		{"nanokvm-board", "enabled", "", false}, {"nanokvm-network", "enabled", "", false},
		{"nanokvm-app", "enabled", "", false}, {"nanokvm-usb", "enabled", "", false},
		{"nanokvm-ssh", "enabled", "/etc/kvm/ssh_stop", false}, {"chronyd", "enabled", "", false},
		{"dnsmasq", "on-demand", "", true}, {"tailscaled", "optional", "", true}, {"netbird", "optional", "", true},
	}
	results := make([]DiagnosticService, len(specs)+1)
	sem := make(chan struct{}, 3)
	var wait sync.WaitGroup
	for index, item := range specs {
		wait.Add(1)
		go func(index int, item spec) {
			defer wait.Done()
			service := DiagnosticService{Name: item.name, Desired: item.desired}
			if item.disabledMarker != "" {
				if _, err := diagnosticStat(item.disabledMarker); err == nil {
					service.Desired = "disabled"
					service.DiagnosticItem = DiagnosticItem{State: "disabled", Detail: "disabled by NanoKVM settings", Optional: item.optional}
					results[index] = service
					return
				}
			}
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				service.DiagnosticItem = DiagnosticItem{State: "unavailable", Detail: "collection budget expired", Optional: item.optional}
				results[index] = service
				return
			}
			service.DiagnosticItem = serviceStatus(ctx, item.name, item.optional)
			results[index] = service
		}(index, item)
	}
	results[len(specs)] = DiagnosticService{Name: "openvpn client", Desired: "optional", DiagnosticItem: openVPNStatus()}
	wait.Wait()
	return results
}

func openVPNStatus() DiagnosticItem {
	item := DiagnosticItem{Optional: true}
	if _, err := diagnosticOpenVPN("openvpn"); err != nil {
		item.State, item.Detail = "absent", "OpenVPN client is not installed"
		return item
	}
	entries, err := diagnosticReadDir(diagnosticOpenVPNRun)
	if err != nil {
		item.State, item.Detail = "stopped", "installed; no active profile reported"
		return item
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".pid") && openVPNPIDLive(filepath.Join(diagnosticOpenVPNRun, entry.Name())) {
			item.State, item.Detail = "running", "an active profile is reported"
			return item
		}
	}
	item.State, item.Detail = "stopped", "installed; no active profile reported"
	return item
}

func dimensions(width, height string) string {
	if width == "" || height == "" {
		return ""
	}
	return width + " × " + height
}

func videoValue(value int) string {
	if value <= 0 {
		return ""
	}
	return strconv.Itoa(value)
}

func monitorProfile() string {
	value := diagnosticText("/etc/kvm/monitor_resolution")
	if value == "0" {
		return "automatic"
	}
	if value == "720" || value == "1080" || value == "1440" {
		return value
	}
	return ""
}

func openVPNPIDLive(path string) bool {
	b, err := diagnosticReadFile(path)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid < 2 {
		return false
	}
	cmd, err := diagnosticReadFile(filepath.Join(diagnosticProcDir, strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return false
	}
	return filepath.Base(strings.Split(string(cmd), "\x00")[0]) == "openvpn"
}

func usbStatus() ([]string, DiagnosticItem) {
	composition := getUSBComposition()
	selected := make([]string, 0, 7)
	for _, item := range []struct {
		name string
		on   bool
	}{{"keyboard", composition.keyboard}, {"relative mouse", composition.relative}, {"absolute mouse", composition.absolute}, {"network", composition.network}, {"disk", composition.disk}, {"serial", composition.serial}, {"audio", composition.audio}} {
		if item.on {
			selected = append(selected, item.name)
		}
	}
	if _, err := diagnosticStat(diagnosticGadgetDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return selected, DiagnosticItem{State: "unavailable", Detail: "USB gadget is not present"}
		}
		return selected, DiagnosticItem{State: "unavailable", Detail: "cannot inspect USB gadget"}
	}
	udc := diagnosticText(filepath.Join(diagnosticGadgetDir, "UDC"))
	if udc == "" {
		return selected, DiagnosticItem{State: "stopped", Detail: "gadget is not bound to a USB controller"}
	}
	state := diagnosticText(filepath.Join("/sys/class/udc", udc, "state"))
	if state == "" {
		return selected, DiagnosticItem{State: "ok", Detail: "gadget bound; host connection state unavailable"}
	}
	return selected, DiagnosticItem{State: "ok", Detail: "gadget bound; controller " + state}
}

func firewallStatus(ctx context.Context) (string, []DiagnosticHookChain, string) {
	if _, err := diagnosticLookPath("nft"); err != nil {
		return "unavailable", []DiagnosticHookChain{}, "nft command is unavailable"
	}
	probe, cancel := context.WithTimeout(ctx, diagnosticsTimeout)
	defer cancel()
	output, err := diagnosticRun(probe, diagnosticsOutput, "nft", "--json", "list", "ruleset")
	if errors.Is(probe.Err(), context.DeadlineExceeded) {
		return "error", []DiagnosticHookChain{}, "nftables query timed out"
	}
	if err != nil {
		return "error", []DiagnosticHookChain{}, "nftables query failed"
	}
	chains, err := parseNFTHookChains(output)
	if err != nil {
		return "error", []DiagnosticHookChain{}, "cannot parse nftables hook chains"
	}
	if len(chains) == 0 {
		return "ok", chains, "no active nftables hook chains"
	}
	return "ok", chains, "active base chains shown; policies alone do not describe every rule"
}

func parseNFTHookChains(data []byte) ([]DiagnosticHookChain, error) {
	var document struct {
		NFTables []struct {
			Chain *struct {
				Family string `json:"family"`
				Table  string `json:"table"`
				Name   string `json:"name"`
				Hook   string `json:"hook"`
				Policy string `json:"policy"`
			} `json:"chain"`
		} `json:"nftables"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	chains := []DiagnosticHookChain{}
	for _, item := range document.NFTables {
		if item.Chain == nil || item.Chain.Hook == "" {
			continue
		}
		chains = append(chains, DiagnosticHookChain{Label: "chain-" + strconv.Itoa(len(chains)+1), Hook: safeNFTHook(item.Chain.Hook), Policy: safeNFTPolicy(item.Chain.Policy)})
	}
	return chains, nil
}

func safeNFTHook(value string) string {
	switch value {
	case "input", "output", "forward", "prerouting", "postrouting", "ingress":
		return value
	}
	return "unknown"
}
func safeNFTPolicy(value string) string {
	switch value {
	case "accept", "drop", "queue", "continue", "return":
		return value
	}
	return ""
}

var errDiagnosticOutputLimit = errors.New("diagnostic command output limit exceeded")

type diagnosticLimitedWriter struct {
	buffer bytes.Buffer
	limit  int
}

func (w *diagnosticLimitedWriter) Write(data []byte) (int, error) {
	remaining := w.limit - w.buffer.Len()
	if remaining <= 0 {
		return 0, errDiagnosticOutputLimit
	}
	if len(data) > remaining {
		_, _ = w.buffer.Write(data[:remaining])
		return remaining, errDiagnosticOutputLimit
	}
	return w.buffer.Write(data)
}

func runDiagnosticCommand(ctx context.Context, limit int, command string, args ...string) ([]byte, error) {
	writer := &diagnosticLimitedWriter{limit: limit}
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Stdout, cmd.Stderr = writer, writer
	cmd.WaitDelay = 250 * time.Millisecond
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	err := cmd.Run()
	if errors.Is(err, errDiagnosticOutputLimit) {
		return writer.buffer.Bytes(), errDiagnosticOutputLimit
	}
	return writer.buffer.Bytes(), err
}
