// nkos-apply-updates applies APK transactions to running services.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const stateDir = "/run/nanokvm-apk"

type stamp struct {
	Inode    uint64
	Device   uint64
	Size     int64
	Modified int64
	Mode     uint32
}
type target struct {
	Name   string
	Kind   string
	Paths  []string
	Trees  []string
	Before map[string]stamp
	PIDs   map[int]string
}
type snapshot struct {
	Targets []target
	Created time.Time
}
type report struct {
	State     string    `json:"state"`
	Restarted []string  `json:"restarted"`
	Deferred  []string  `json:"deferred"`
	Failed    []string  `json:"failed"`
	Finished  time.Time `json:"finished"`
}
type process struct {
	PID                       int
	Exe, Arg0, Name, Identity string
	Maps                      []string
}

func exists(p string) bool { _, e := os.Stat(p); return e == nil }
func fingerprint(paths, trees []string) map[string]stamp {
	out := map[string]stamp{}
	add := func(p string) {
		i, e := os.Stat(p)
		if e != nil || i.IsDir() {
			return
		}
		st, ok := i.Sys().(*syscall.Stat_t)
		if !ok {
			return
		}
		out[p] = stamp{st.Ino, uint64(st.Dev), i.Size(), i.ModTime().UnixNano(), uint32(i.Mode())}
	}
	for _, p := range paths {
		add(p)
	}
	for _, root := range trees {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
			if e == nil && !d.IsDir() {
				add(p)
			}
			return nil
		})
	}
	return out
}
func identity(pid int) string {
	b, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if e != nil {
		return ""
	}
	_, tail, ok := strings.Cut(string(b), ") ")
	if !ok {
		return ""
	}
	fields := strings.Fields(tail)
	if len(fields) < 20 {
		return ""
	}
	return fields[19]
}
func processes() []process {
	dirs, _ := filepath.Glob("/proc/[0-9]*")
	var result []process
	for _, d := range dirs {
		pid, _ := strconv.Atoi(filepath.Base(d))
		exe, e := os.Readlink(d + "/exe")
		if e != nil {
			continue
		}
		cmd, _ := os.ReadFile(d + "/cmdline")
		arg0 := strings.Split(string(cmd), "\x00")[0]
		comm, _ := os.ReadFile(d + "/comm")
		p := process{PID: pid, Exe: strings.TrimSuffix(exe, " (deleted)"), Arg0: arg0, Name: strings.TrimSpace(string(comm)), Identity: identity(pid)}
		maps, _ := os.ReadFile(d + "/maps")
		for _, line := range strings.Split(string(maps), "\n") {
			f := strings.Fields(line)
			if len(f) >= 6 && strings.HasPrefix(f[5], "/") && (strings.Contains(f[1], "x") || strings.Contains(f[5], ".so")) {
				p.Maps = append(p.Maps, f[5])
			}
		}
		result = append(result, p)
	}
	return result
}
func addProcess(t *target, p process) {
	t.Paths = append(t.Paths, p.Exe)
	t.Paths = append(t.Paths, p.Maps...)
	if t.PIDs == nil {
		t.PIDs = map[int]string{}
	}
	t.PIDs[p.PID] = p.Identity
}

// Only restart the active profile owned by NanoKVM, never an arbitrary
// manually started OpenVPN process.
func managedVPN(pid int) bool {
	b, e := os.ReadFile("/etc/kvm/openvpn/active")
	if e != nil {
		return false
	}
	id := strings.TrimSpace(string(b))
	if id == "" || strings.ContainsAny(id, "/\\. \t\n") {
		return false
	}
	b, e = os.ReadFile("/run/nkos-openvpn/" + id + ".pid")
	if e != nil {
		return false
	}
	stored, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if stored != pid {
		return false
	}
	b, e = os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if e != nil {
		return false
	}
	args := strings.Split(string(b), "\x00")
	for i, a := range args {
		if a == "--config" && i+1 < len(args) && args[i+1] == "/etc/kvm/openvpn/"+id+".ovpn" {
			return true
		}
	}
	return false
}

func gather() snapshot {
	all := processes()
	s := snapshot{Created: time.Now()}
	started, _ := filepath.Glob("/run/openrc/started/*")
	for _, entry := range started {
		name := filepath.Base(entry)
		t := target{Name: name, Kind: "service", Paths: []string{"/etc/init.d/" + name, "/etc/conf.d/" + name}}
		records, _ := filepath.Glob("/run/openrc/daemons/" + name + "/*")
		for _, record := range records {
			b, _ := os.ReadFile(record)
			values := map[string]string{}
			for _, line := range strings.Split(string(b), "\n") {
				k, v, ok := strings.Cut(line, "=")
				if ok {
					values[k] = v
					if k == "exec" || (strings.HasPrefix(k, "argv_") && strings.HasPrefix(v, "/etc/")) {
						t.Paths = append(t.Paths, v)
					}
				}
			}
			pidraw, _ := os.ReadFile(values["pidfile"])
			pid, _ := strconv.Atoi(strings.TrimSpace(string(pidraw)))
			for _, p := range all {
				if (pid > 1 && p.PID == pid) || (pid == 0 && values["exec"] != "" && (p.Exe == values["exec"] || p.Arg0 == values["exec"])) {
					addProcess(&t, p)
				}
			}
		}
		switch name {
		case "nanokvm-app":
			t.Trees = []string{"/kvmapp/server", "/kvmapp/kvm_system", "/usr/share/nanokvm/edid"}
			t.Paths = append(t.Paths, "/usr/libexec/nanokvm/legacy/S95nanokvm", "/usr/sbin/nanokvm_update_edid")
			for _, p := range all {
				if p.Name == "NanoKVM-Server" || p.Name == "kvm_system" {
					addProcess(&t, p)
				}
			}
		case "nanokvm-network":
			for _, n := range []string{"S25wifimod", "S30eth", "S30wifi"} {
				t.Paths = append(t.Paths, "/usr/libexec/nanokvm/legacy/"+n)
			}
			for _, p := range all {
				if p.Name == "wpa_supplicant" || p.Name == "udhcpc" {
					addProcess(&t, p)
				}
			}
		case "nanokvm-policy":
			for _, n := range []string{"S29qdisc", "S34mssclamp", "S38memory", "S49persistent-cron", "S94sg2002aes"} {
				t.Paths = append(t.Paths, "/usr/libexec/nanokvm/legacy/"+n)
			}
		case "nanokvm-watchdog":
			t.Paths = append(t.Paths, "/usr/libexec/nanokvm/legacy/S13nanokvm-watchdog")
			t.Kind = "reboot"
		case "nanokvm-board", "nanokvm-modules", "nanokvm-storage", "nanokvm-usb", "dbus", "udev", "udev-trigger", "devfs", "sysfs", "procfs", "root", "localmount", "hwclock", "sysctl", "modules", "hwdrivers", "bootmisc", "hostname":
			t.Kind = "reboot"
		}
		if len(records) == 0 && !strings.HasPrefix(name, "nanokvm-") {
			for _, p := range all {
				if p.Name == name || filepath.Base(p.Arg0) == name {
					addProcess(&t, p)
				}
			}
			if len(t.PIDs) == 0 {
				t.Kind = "reboot"
			}
		}
		t.Before = fingerprint(t.Paths, t.Trees)
		s.Targets = append(s.Targets, t)
	}
	// Legacy optional services have no OpenRC daemon records.
	for _, spec := range []struct{ name, comm, script string }{{"tailscaled", "tailscaled", "S98tailscaled"}, {"dnsmasq", "dnsmasq", "S80dnsmasq"}, {"picoclaw", "picoclaw", "S96picoclaw"}} {
		found := false
		for _, t := range s.Targets {
			if t.Name == spec.name {
				found = true
			}
		}
		if found {
			continue
		}
		t := target{Name: spec.name, Kind: "legacy", Paths: []string{"/usr/libexec/nanokvm/legacy/" + spec.script}}
		for _, p := range all {
			if p.Name == spec.comm {
				addProcess(&t, p)
			}
		}
		if len(t.PIDs) > 0 {
			t.Before = fingerprint(t.Paths, nil)
			s.Targets = append(s.Targets, t)
		}
	}
	vpn := target{Name: "openvpn", Kind: "openvpn", Paths: []string{"/usr/sbin/openvpn3"}}
	for _, p := range all {
		if p.Name == "openvpn3" && managedVPN(p.PID) {
			addProcess(&vpn, p)
		}
	}
	if len(vpn.PIDs) > 0 {
		vpn.Before = fingerprint(vpn.Paths, nil)
		s.Targets = append(s.Targets, vpn)
	}
	for _, t := range []target{
		{Name: "kernel boot payload", Kind: "reboot", Trees: []string{"/usr/lib/nanokvm/boot"}},
		{Name: "kernel modules and device firmware", Kind: "reboot", Trees: []string{"/lib/modules", "/lib/firmware", "/usr/share/fw_vcodec"}},
		{Name: "core runtime", Kind: "reboot", Paths: []string{"/bin/busybox", "/sbin/init", "/sbin/openrc", "/sbin/openrc-run"}},
	} {
		t.Before = fingerprint(t.Paths, t.Trees)
		s.Targets = append(s.Targets, t)
	}
	for _, p := range all {
		if p.PID == 1 {
			t := target{Name: "PID 1 libraries", Kind: "reboot"}
			addProcess(&t, p)
			t.Before = fingerprint(t.Paths, nil)
			s.Targets = append(s.Targets, t)
		}
	}
	return s
}
func changed(t target) bool { return !reflect.DeepEqual(t.Before, fingerprint(t.Paths, t.Trees)) }
func writeJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	if e = os.WriteFile(path+".tmp", append(b, '\n'), 0600); e != nil {
		return e
	}
	return os.Rename(path+".tmp", path)
}
func run(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, args[0], args[1:]...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
func running(t target) bool {
	if t.Kind == "service" {
		return exists("/run/openrc/started/" + t.Name)
	}
	for pid, id := range t.PIDs {
		if id != "" && identity(pid) == id {
			return true
		}
	}
	return false
}
func stopVPN(t target) error {
	for pid, id := range t.PIDs {
		if id == "" || identity(pid) != id {
			continue
		}
		if e := syscall.Kill(pid, syscall.SIGTERM); e != nil && !errors.Is(e, syscall.ESRCH) {
			return e
		}
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if !running(t) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("OpenVPN did not exit; retained process, manual restart required")
}
func plan(s snapshot, isRunning func(target) bool) ([]target, []string) {
	var deferred []string
	var todo []target
	appNeeded := false
	appRunning := false
	for _, t := range s.Targets {
		if t.Name == "nanokvm-app" && isRunning(t) {
			appRunning = true
		}
	}
	for _, t := range s.Targets {
		if !changed(t) {
			continue
		}
		if t.Kind == "reboot" || t.Kind == "firmware" {
			deferred = append(deferred, t.Name)
			continue
		}
		if t.Kind == "openvpn" && !appRunning {
			deferred = append(deferred, "OpenVPN: application stopped; reconnect profile manually")
			continue
		}
		if isRunning(t) {
			todo = append(todo, t)
			if t.Kind == "openvpn" {
				appNeeded = true
			}
		}
	}
	if appNeeded {
		found := false
		for _, t := range todo {
			if t.Name == "nanokvm-app" {
				found = true
			}
		}
		if !found {
			for _, t := range s.Targets {
				if t.Name == "nanokvm-app" && isRunning(t) {
					todo = append(todo, t)
				}
			}
		}
	}
	return todo, deferred
}
func openRCOrder() (map[string]int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// rc-status processes -s immediately, so formatting must precede it.
	b, e := exec.CommandContext(ctx, "rc-status", "--nocolor", "--format", "ini", "--servicelist").Output()
	if e != nil {
		return nil, fmt.Errorf("cannot obtain OpenRC dependency order: %w", e)
	}
	result := map[string]int{}
	for _, line := range strings.Split(string(b), "\n") {
		name, _, ok := strings.Cut(line, "=")
		if ok {
			result[strings.TrimSpace(name)] = len(result) + 1
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("OpenRC returned an empty dependency order")
	}
	return result, nil
}
func orderTargets(todo []target, order map[string]int) {
	rank := func(t target) int {
		if t.Kind == "openvpn" {
			return 0
		}
		if n, ok := order[t.Name]; ok {
			return n
		}
		return len(order) + 1
	}
	sort.SliceStable(todo, func(i, j int) bool { return rank(todo[i]) < rank(todo[j]) })
}
func renewedByOpenRC(name string, before map[int]string) bool {
	if len(before) == 0 {
		return false
	}
	for pid, id := range before {
		if id != "" && identity(pid) == id {
			return false
		}
	}
	// The old daemon(s) exited. Confirm replacements actually exist rather
	// than mistaking a crashed service or an in-progress start for success.
	for _, t := range gather().Targets {
		if t.Name == name && len(t.PIDs) > 0 {
			return true
		}
	}
	return false
}
func waitOpenRC(name string) error {
	deadline := time.Now().Add(90 * time.Second)
	for exists("/run/openrc/starting/"+name) || exists("/run/openrc/stopping/"+name) {
		if time.Now().After(deadline) {
			return fmt.Errorf("OpenRC transition timed out: %s", name)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil
}
func apply(s snapshot) report {
	r := report{State: "applied"}
	todo, deferred := plan(s, running)
	r.Deferred = deferred
	// OpenRC supplies the dependency order; do not duplicate its graph or
	// disable its normal cascading restart semantics.
	order, err := openRCOrder()
	if err != nil {
		r.State = "failed"
		r.Failed = append(r.Failed, err.Error())
		r.Finished = time.Now()
		return r
	}
	orderTargets(todo, order)
	baseline := map[string]map[int]string{}
	for _, t := range gather().Targets {
		baseline[t.Name] = t.PIDs
	}
	for _, t := range todo {
		if t.Kind == "service" {
			if e := waitOpenRC(t.Name); e != nil {
				r.Failed = append(r.Failed, e.Error())
				continue
			}
		}
		if !running(t) {
			continue
		}
		if t.Kind == "service" && renewedByOpenRC(t.Name, baseline[t.Name]) {
			fmt.Println("Already restarted by OpenRC dependency handling:", t.Name)
			r.Restarted = append(r.Restarted, t.Name)
			continue
		}
		fmt.Println("Applying APK update:", t.Name)
		var e error
		switch t.Kind {
		case "service":
			e = run("rc-service", t.Name, "restart")
		case "legacy":
			e = run(t.Paths[0], "stop")
			if e == nil {
				e = run(t.Paths[0], "start")
			}
		case "openvpn":
			e = stopVPN(t)
		}
		if e != nil {
			r.Failed = append(r.Failed, t.Name+": "+e.Error())
		} else {
			r.Restarted = append(r.Restarted, t.Name)
		}
	}
	if len(r.Deferred) > 0 {
		// APK activates the kernel before this worker runs; only reboot remains.
		f, e := os.OpenFile("/run/reboot-required.pkgs", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if e == nil {
			for _, n := range r.Deferred {
				fmt.Fprintln(f, n)
			}
			f.Close()
			_ = os.WriteFile("/run/reboot-required", []byte("System components changed; see reboot-required.pkgs\n"), 0644)
		} else {
			r.Failed = append(r.Failed, e.Error())
		}
	}
	if len(r.Failed) > 0 {
		r.State = "failed"
	} else if len(r.Deferred) > 0 {
		r.State = "reboot-required"
	}
	r.Finished = time.Now()
	return r
}
func main() {
	if e := mainErr(); e != nil {
		fmt.Fprintln(os.Stderr, "NanoKVM APK apply:", e)
		os.Exit(1)
	}
}
func mainErr() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: nkos-apply-updates pre-commit|post-commit|apply|status")
	}
	if os.Args[1] == "status" {
		b, e := os.ReadFile(stateDir + "/result.json")
		if e == nil {
			fmt.Print(string(b))
		}
		return e
	}
	if !exists("/run/openrc/softlevel") {
		return nil
	}
	if e := os.MkdirAll(stateDir, 0700); e != nil {
		return e
	}
	lock, e := os.OpenFile(stateDir+"/lock", os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer lock.Close()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return fmt.Errorf("previous APK service application is still running: %w", e)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	path := stateDir + "/snapshot.json"
	switch os.Args[1] {
	case "pre-commit":
		if exists(stateDir + "/pending") {
			return fmt.Errorf("previous APK application pending; inspect %s/result.json and run nkos-apply-updates apply", stateDir)
		}
		return writeJSON(path, gather())
	case "post-commit":
		if !exists(path) {
			return nil
		} // First installation of this hook, or offline root.
		if e = os.WriteFile(stateDir+"/pending", nil, 0600); e != nil {
			return e
		}
		if e = writeJSON(stateDir+"/result.json", report{State: "pending"}); e != nil {
			return e
		}
		log, e := os.OpenFile("/var/log/nanokvm-apk-apply.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		defer log.Close()
		exe, e := os.Executable()
		if e != nil {
			return e
		}
		cmd := exec.Command(exe, "apply")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		cmd.Stdout = log
		cmd.Stderr = log
		// Release before starting the worker; pending prevents a second transaction.
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		if e = cmd.Start(); e != nil {
			return e
		}
		_ = cmd.Process.Release()
		fmt.Println("NanoKVM: applying changed components; nkos-apply-updates status shows the result")
		return nil
	case "apply":
		var s snapshot
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if e = json.Unmarshal(b, &s); e != nil {
			return e
		}
		result := apply(s)
		if e = writeJSON(stateDir+"/result.json", result); e != nil {
			return e
		}
		_ = os.Remove(path)
		_ = os.Remove(stateDir + "/pending")
		if len(result.Failed) > 0 {
			return fmt.Errorf("component restart failed: %s", strings.Join(result.Failed, "; "))
		}
		return nil
	default:
		return fmt.Errorf("unknown operation")
	}
}
