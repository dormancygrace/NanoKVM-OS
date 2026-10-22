package recovery

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const PendingPath = "var/lib/nanokvm-recovery/pending.json"
const statusPath = "var/lib/nanokvm-recovery/status.json"

type Options struct {
	Action    string `json:"action"` // restore or reset
	Mode      string `json:"mode"`   // device or migrate
	KeepUsers bool   `json:"keepUsers"`
	KeepApps  bool   `json:"keepApps"`
}
type Change struct {
	Path   string `json:"path"`
	Before *File  `json:"before"`
	After  *File  `json:"after"`
}
type Plan struct {
	Started        bool     `json:"started"`
	Options        Options  `json:"options"`
	Changes        []Change `json:"changes"`
	RemovePackages []string `json:"removePackages"`
	// The source world is checked again at boot: do not execute a stale removal.
	World []byte `json:"world"`
}
type Preview struct {
	Token          string   `json:"token"`
	Changed        []string `json:"changed"`
	Skipped        []string `json:"skipped"`
	RemovePackages []string `json:"removePackages"`
	Packages       []string `json:"packages"`
	Version        string   `json:"version"`
}
type Status struct {
	State   string `json:"state"`
	Message string `json:"message"`
}

// Engine's root is injectable for filesystem tests. Production always uses /.
type Engine struct {
	Root      string
	Version   string
	Board     string
	writeFile func(string, *File) error
}

func (e Engine) applyFile(name string, f *File) error {
	if e.writeFile != nil {
		return e.writeFile(name, f)
	}
	return write(e.Root, name, f)
}
func (e Engine) Snapshot() (Archive, error) {
	a := Archive{Format: 1, Version: e.Version, Board: e.Board, Created: time.Now().UTC()}
	var err error
	a.Files, err = inventory(e.Root)
	if err != nil {
		return a, err
	}
	world, err := read(e.Root, "etc/apk/world", MaxFile)
	if err != nil {
		return a, err
	}
	if world != nil {
		for _, line := range strings.Fields(string(world.Data)) {
			parts := strings.FieldsFunc(line, func(r rune) bool { return strings.ContainsRune("<>=~@", r) })
			if len(parts) == 0 {
				return a, errors.New("invalid APK world")
			}
			name := parts[0]
			if !packageName.MatchString(name) {
				return a, errors.New("unsupported package name in world")
			}
			a.Packages = append(a.Packages, name)
		}
	}
	shadow, err := read(e.Root, "etc/shadow", MaxFile)
	if err != nil {
		return a, err
	}
	if shadow != nil {
		for _, line := range strings.Split(string(shadow.Data), "\n") {
			fields := strings.Split(line, ":")
			if len(fields) > 1 && fields[0] == "root" {
				a.RootHash = fields[1]
			}
		}
	}
	return a, Validate(a)
}
func compatible(from, to string) bool {
	a, b := versionPattern.FindStringSubmatch(from), versionPattern.FindStringSubmatch(to)
	if a == nil || b == nil || a[1] != b[1] {
		return false
	}
	for i := 2; i <= 3; i++ {
		x, _ := strconv.Atoi(a[i])
		y, _ := strconv.Atoi(b[i])
		if x != y {
			return x < y
		}
	}
	return true
}
func (e Engine) Prepare(a *Archive, o Options) (Plan, Preview, error) {
	p := Plan{Options: o}
	v := Preview{Changed: []string{}, Skipped: []string{}, Packages: []string{}, RemovePackages: []string{}}
	if o.Action != "restore" && o.Action != "reset" {
		return p, v, errors.New("invalid action")
	}
	if o.Action == "restore" {
		if a == nil {
			return p, v, errors.New("archive required")
		}
		if err := Validate(*a); err != nil {
			return p, v, err
		}
		if !compatible(a.Version, e.Version) {
			return p, v, errors.New("install the same or a newer application version in the same major series before restoring")
		}
		if o.Mode != "device" && o.Mode != "migrate" {
			return p, v, errors.New("invalid restore mode")
		}
		if o.Mode == "device" && (a.Board == "" || a.Board != e.Board) {
			return p, v, errors.New("board profile differs or is unknown; use migration mode")
		}
		v.Version = a.Version
		v.Packages = a.Packages
	}
	current, err := inventory(e.Root)
	if err != nil {
		return p, v, err
	}
	desired := map[string]*File{}
	for _, f := range current {
		if o.Action == "reset" && o.KeepUsers && (users(f.Path) || f.Path == "etc/kvm/ssh_stop" || strings.HasPrefix(f.Path, "etc/ssh/")) {
			continue
		}
		if o.Action == "restore" && o.Mode == "migrate" && !portable(f.Path) {
			continue
		}
		desired[f.Path] = nil
	}
	if o.Action == "restore" {
		for _, f := range a.Files {
			if o.Mode == "migrate" && !portable(f.Path) {
				v.Skipped = append(v.Skipped, f.Path)
				continue
			}
			// Do not copy private files with broad permissions from FAT or old images.
			f.Mode = 0600
			if f.Path == "etc/hosts" || f.Path == "etc/hostname" || f.Path == "etc/timezone" || f.Path == "etc/localtime" || f.Path == "etc/chrony.conf" {
				f.Mode = 0644
			}
			if strings.HasPrefix(f.Path, "etc/kvm/scripts/") {
				f.Mode = 0700
			}
			desired[f.Path] = &f
		}
	}
	put := func(name, value string, mode uint32) {
		desired[name] = &File{Path: name, Data: []byte(value), Mode: mode}
	}
	// The marker is a layout invariant, not a user setting. Never resurrect
	// obsolete /boot USB flags after a reset or restore.
	if o.Action == "reset" || o.Mode == "device" {
		put("etc/kvm/usb/.layout-v1", "1\n", 0600)
	}
	// Invalidate all previously issued web sessions, including custom JWT keys.
	if o.Action == "restore" && o.Mode == "migrate" {
		cfg, err := read(e.Root, "etc/kvm/server.yaml", MaxFile)
		if err != nil {
			return p, v, err
		}
		if cfg != nil {
			desired[cfg.Path] = cfg
		}
	}
	desired["etc/kvm/.jwt_secret"] = nil
	if o.Action == "reset" || o.Mode == "device" {
		desired["etc/kvm/.picoclaw_internal_token"] = nil
	}
	if f, ok := desired["etc/kvm/server.yaml"]; ok && f != nil {
		var cfg map[string]any
		if err = yaml.Unmarshal(f.Data, &cfg); err != nil {
			return p, v, err
		}
		delete(cfg, "jwt")
		cfg["authentication"] = "enable"
		f.Data, err = yaml.Marshal(cfg)
		if err != nil {
			return p, v, err
		}
	}
	if o.Action == "reset" {
		// Keep unrelated cron jobs; disable only our automatic index refresh.
		cron, err := read(e.Root, "etc/crontabs/root", MaxFile)
		if err != nil {
			return p, v, err
		}
		if cron != nil {
			lines := []string{}
			for _, line := range strings.Split(string(cron.Data), "\n") {
				fields := strings.Fields(line)
				if strings.HasPrefix(line, "# nanokvm-apk-index-refresh ") || (len(fields) == 7 && fields[5] == "/usr/sbin/nkos-update" && fields[6] == "refresh-indexes") {
					continue
				}
				lines = append(lines, line)
			}
			put("etc/crontabs/root", strings.Join(lines, "\n"), 0600)
		}
		// S10uuid needs these files to exist before it applies the chip hostname.
		put("etc/hostname", "nanokvm\n", 0644)
		put("etc/hosts", "127.0.0.1 localhost nanokvm\n::1 localhost\n", 0644)
		put("etc/timezone", "UTC\n", 0644)
		tz, err := os.ReadFile(e.Root + "/usr/share/zoneinfo/UTC")
		if err != nil {
			return p, v, err
		}
		put("etc/localtime", string(tz), 0644)
		put("etc/chrony.conf", "pool pool.ntp.org iburst\ndriftfile /var/lib/chrony/drift\nmakestep 1.0 3\nrtcsync\nport 0\ncmdport 0\nbindcmdaddress /run/chrony/chronyd.sock\n", 0644)
		if !o.KeepUsers {
			put("etc/kvm/ssh_stop", "", 0600)
		}
	}
	// Root alone is merged into shadow. Service accounts belong to APK.
	if (o.Action == "reset" && !o.KeepUsers) || (o.Action == "restore" && o.Mode == "device" && a.RootHash != "") {
		shadow, err := read(e.Root, "etc/shadow", MaxFile)
		if err != nil {
			return p, v, err
		}
		if shadow == nil {
			return p, v, errors.New("root password database missing")
		}
		hash := "!"
		if a != nil {
			hash = a.RootHash
		}
		lines := strings.Split(string(shadow.Data), "\n")
		found := false
		for i, line := range lines {
			fields := strings.Split(line, ":")
			if len(fields) >= 2 && fields[0] == "root" {
				fields[1] = hash
				lines[i] = strings.Join(fields, ":")
				found = true
			}
		}
		if !found {
			return p, v, errors.New("root account missing")
		}
		put("etc/shadow", strings.Join(lines, "\n"), 0600)
	}
	if o.Action == "reset" && !o.KeepApps {
		world, err := read(e.Root, "etc/apk/world", MaxFile)
		if err != nil {
			return p, v, err
		}
		if world == nil {
			return p, v, errors.New("APK world missing")
		}
		p.World = world.Data
		p.RemovePackages, err = removable(world.Data)
		if err != nil {
			return p, v, err
		}
		v.RemovePackages = p.RemovePackages
	}
	names := make([]string, 0, len(desired))
	for name := range desired {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		before, err := read(e.Root, name, MaxFile)
		if err != nil {
			return p, v, fmt.Errorf("%s: %w", name, err)
		}
		after := desired[name]
		if before == nil && after == nil {
			continue
		}
		p.Changes = append(p.Changes, Change{Path: name, Before: before, After: after})
		if before == nil || after == nil || !bytes.Equal(before.Data, after.Data) || before.Mode != after.Mode {
			v.Changed = append(v.Changed, name)
		}
	}
	data, err := json.Marshal(p)
	if err != nil {
		return p, v, err
	}
	digest := sha256.Sum256(data)
	v.Token = hex.EncodeToString(digest[:])
	return p, v, nil
}
func removable(world []byte) ([]string, error) {
	// nanokvm-release keeps the complete OS dependency closure installed. Never
	// use --recursive: removing optional world roots must not remove dependents.
	protected := map[string]bool{"nanokvm-release": true, "alpine-base": true, "alpine-baselayout": true, "alpine-keys": true, "apk-tools": true, "busybox": true, "libc-utils": true}
	result := []string{}
	release := false
	for _, line := range strings.Fields(string(world)) {
		fields := strings.FieldsFunc(line, func(r rune) bool { return strings.ContainsRune("<>=~@", r) })
		if len(fields) == 0 || !packageName.MatchString(fields[0]) {
			return nil, errors.New("unsupported APK world entry")
		}
		name := fields[0]
		if name == "nanokvm-release" {
			release = true
		}
		if protected[name] || (strings.HasPrefix(name, "nanokvm") && !strings.HasPrefix(name, "nanokvm-rustdesk")) {
			continue
		}
		result = append(result, name)
	}
	if !release {
		return nil, errors.New("factory package reset requires the nanokvm-release system package")
	}
	sort.Strings(result)
	return result, nil
}
func (e Engine) Pending() bool {
	_, err := os.Lstat(e.Root + "/" + PendingPath)
	return !errors.Is(err, os.ErrNotExist)
}
func (e Engine) Status() Status {
	if e.Pending() {
		return Status{State: "pending", Message: "Recovery is queued; reboot to apply it."}
	}
	f, err := read(e.Root, statusPath, 4096)
	if err != nil {
		return Status{State: "error", Message: err.Error()}
	}
	s := Status{State: "idle"}
	if f != nil {
		_ = json.Unmarshal(f.Data, &s)
	}
	return s
}
func (e Engine) Stage(p Plan) error {
	if e.Pending() {
		return errors.New("a recovery operation is already pending")
	}
	if err := validatePlan(p); err != nil {
		return err
	}
	for _, c := range p.Changes {
		f, err := read(e.Root, c.Path, MaxFile)
		if err != nil {
			return err
		}
		x, _ := json.Marshal(f)
		y, _ := json.Marshal(c.Before)
		if !bytes.Equal(x, y) {
			return errors.New("settings changed since preview; preview again")
		}
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if len(data) > 2*MaxArchive {
		return errors.New("recovery journal too large")
	}
	return write(e.Root, PendingPath, &File{Data: data, Mode: 0600})
}
func validatePlan(p Plan) error {
	if p.Options.Action != "restore" && p.Options.Action != "reset" {
		return errors.New("invalid recovery journal")
	}
	if len(p.Changes) > MaxFiles+8 {
		return errors.New("excessive recovery changes")
	}
	seen := map[string]bool{}
	total := 0
	for _, c := range p.Changes {
		if (!allowed(c.Path) && c.Path != "etc/shadow" && c.Path != "etc/kvm/.jwt_secret" && c.Path != "etc/kvm/.picoclaw_internal_token") || seen[c.Path] {
			return errors.New("invalid recovery destination")
		}
		seen[c.Path] = true
		for _, f := range []*File{c.Before, c.After} {
			if f != nil {
				total += len(f.Data)
				if f.Path != c.Path || len(f.Data) > MaxFile || f.Mode&^0777 != 0 {
					return errors.New("invalid recovery file")
				}
			}
		}
	}
	if total > 2*MaxData+2*MaxFile {
		return errors.New("excessive recovery data")
	}
	if len(p.World) > 0 {
		names, err := removable(p.World)
		if err != nil {
			return err
		}
		a, _ := json.Marshal(names)
		b, _ := json.Marshal(p.RemovePackages)
		if !bytes.Equal(a, b) || p.Options.Action != "reset" || p.Options.KeepApps {
			return errors.New("invalid package reset")
		}
	} else if len(p.RemovePackages) > 0 {
		return errors.New("missing package reset inventory")
	}
	return nil
}

// Resume is called only during early boot, with services stopped. A power loss
// leaves the durable plan for replay. An ordinary write failure restores all
// previous settings before allowing normal startup. APK removals are not undone.
func (e Engine) Resume(remove func([]string) error) error {
	f, err := read(e.Root, PendingPath, 2*MaxArchive)
	if err != nil {
		return err
	}
	if f == nil {
		return nil
	}
	var p Plan
	if err = json.Unmarshal(f.Data, &p); err != nil {
		return err
	}
	if err = validatePlan(p); err != nil {
		return err
	}
	// Services may flush their settings while shutting down. Capture that final
	// state once, before touching packages/files, and persist it for crash replay.
	if !p.Started {
		if p.Options.Action == "reset" {
			next, _, err := e.Prepare(nil, p.Options)
			if err != nil {
				return err
			}
			next.World = p.World
			next.RemovePackages = p.RemovePackages
			p = next
		} else {
			seen := map[string]bool{}
			for i := range p.Changes {
				c := &p.Changes[i]
				seen[c.Path] = true
				c.Before, err = read(e.Root, c.Path, MaxFile)
				if err != nil {
					return err
				}
			}
			current, err := inventory(e.Root)
			if err != nil {
				return err
			}
			for _, f := range current {
				if seen[f.Path] || (p.Options.Mode == "migrate" && !portable(f.Path)) {
					continue
				}
				p.Changes = append(p.Changes, Change{Path: f.Path, Before: &f})
			}
		}
		p.Started = true
		if err = validatePlan(p); err != nil {
			return err
		}
		data, err := json.Marshal(p)
		if err != nil {
			return err
		}
		if err = write(e.Root, PendingPath, &File{Data: data, Mode: 0600}); err != nil {
			return err
		}
	}
	var operationErr error
	if p.Options.Action == "reset" && !p.Options.KeepApps {
		world, err := read(e.Root, "etc/apk/world", MaxFile)
		if err != nil {
			return err
		}
		// After an interrupted deletion APK may already have removed some roots;
		// replay only the approved names, never discover additional removals here.
		if world == nil {
			return errors.New("APK world missing during recovery")
		}
		names, e := remainingRemovals(p.World, world.Data, p.RemovePackages)
		if e != nil {
			operationErr = e
		} else {
			operationErr = remove(names)
		}
	}
	if operationErr == nil {
		for _, c := range p.Changes {
			if err = e.applyFile(c.Path, c.After); err != nil {
				operationErr = fmt.Errorf("%s: %w", c.Path, err)
				break
			}
		}
	}
	if operationErr != nil {
		for _, c := range p.Changes {
			if err = e.applyFile(c.Path, c.Before); err != nil {
				return fmt.Errorf("recovery and rollback failed at %s: %w", c.Path, err)
			}
		}
	}
	status := Status{State: "complete", Message: "Recovery completed."}
	if operationErr != nil {
		status = Status{State: "failed", Message: "Settings restored after a recovery error: " + operationErr.Error() + ". Some optional packages may have been removed."}
	}
	if len(status.Message) > 3000 {
		status.Message = status.Message[:3000]
	}
	data, _ := json.Marshal(status)
	if err = write(e.Root, statusPath, &File{Data: data, Mode: 0600}); err != nil {
		return err
	}
	return write(e.Root, PendingPath, nil)
}

func (o *Options) UnmarshalJSON(data []byte) error {
	type plain Options
	var v plain
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	if v.Action == "reset" {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return err
		}
		for _, key := range []string{"keepUsers", "keepApps"} {
			if len(fields[key]) == 0 || string(fields[key]) == "null" {
				return errors.New("reset requires explicit keepUsers and keepApps choices")
			}
		}
	}
	*o = Options(v)
	return nil
}

// A retry may have fewer optional roots, but never new or changed roots.
func remainingRemovals(original, current []byte, approved []string) ([]string, error) {
	if _, err := removable(current); err != nil {
		return nil, err
	}
	old := map[string]bool{}
	for _, line := range strings.Fields(string(original)) {
		old[line] = true
	}
	present := map[string]bool{}
	for _, line := range strings.Fields(string(current)) {
		if !old[line] {
			return nil, errors.New("APK world changed after recovery was queued")
		}
		name := strings.FieldsFunc(line, func(r rune) bool { return strings.ContainsRune("<>=~@", r) })[0]
		present[name] = true
	}
	result := []string{}
	for _, name := range approved {
		if present[name] {
			result = append(result, name)
		}
	}
	return result, nil
}
