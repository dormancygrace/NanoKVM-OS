package network

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"NanoKVM-Server/proto"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// A network change that can cut the device off runs on trial: it is applied
// at once, and unless it is confirmed within trialDuration, from any address
// the device can still be reached at, the previous settings come back. The
// page that made the change looks for the device at the addresses the trial
// lists and confirms from there, also from a new address with a one-time
// token. The previous settings are kept on the boot partition, so a restart
// during the trial, also a power cycle by a user who lost access, reverts
// too.
var trialDuration = 2 * time.Minute
var trialFile = "/boot/net-trial.json"

// A restore that fails is tried again this often; until it succeeds, no
// other change starts, and the record stays the way back.
var trialRetry = 30 * time.Second

// The record of a trial: the previous contents of each settings file, null
// for a file that did not exist, and what restarts their service.
type trialRecord struct {
	Files   map[string]*string `json:"files"`
	Service string             `json:"service"`
}

type trial struct {
	id, token string
	deadline  time.Time
	record    trialRecord
	timer     *time.Timer
	// Set once the service has restarted with the change: until then the
	// device still answers with its previous settings, and reaching it
	// proves nothing.
	isApplied bool
	// Set while the previous settings are being written back, also while a
	// failed restore waits for its retry.
	reverting bool
	retry     *time.Timer
}

var (
	trialMu sync.Mutex
	active  *trial
)

// The services whose settings run on trial, by the name in their record.
var trialRestarts = map[string]func() error{
	"ethernet": func() error { return restartEthernet() },
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func snapshotFiles(paths []string) (map[string]*string, error) {
	files := make(map[string]*string, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			files[path] = nil
			continue
		}
		if err != nil {
			return nil, err
		}
		contents := string(data)
		files[path] = &contents
	}
	return files, nil
}

func restoreFiles(files map[string]*string) error {
	var errs []error
	for path, contents := range files {
		if contents == nil {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
			continue
		}
		errs = append(errs, writeFileAtomic(path, []byte(*contents)))
	}
	return errors.Join(errs...)
}

// writeFileAtomic replaces path with data, synced before the rename: on the
// FAT boot partition a crash would otherwise leave an empty or torn file.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if syncErr := f.Sync(); err == nil {
		err = syncErr
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func syncFiles() error { return exec.Command("sync").Run() }

// beginTrial records the current contents of the files a change is about to
// write. Only one change runs on trial at a time, and none while an earlier
// one is being undone.
func beginTrial(service string, paths []string) (*trial, error) {
	trialMu.Lock()
	defer trialMu.Unlock()
	if active != nil {
		return nil, errors.New("a network change is waiting for confirmation; confirm it or wait for it to be undone")
	}
	// A record nobody owns is the only way back to the confirmed settings.
	if _, err := os.Lstat(trialFile); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("an earlier network change is still being undone; try again later")
	}
	files, err := snapshotFiles(paths)
	if err != nil {
		return nil, fmt.Errorf("cannot record the current network settings: %w", err)
	}
	t := &trial{id: randomHex(8), token: randomHex(16), record: trialRecord{Files: files, Service: service}}
	data, err := json.Marshal(t.record)
	if err != nil {
		return nil, err
	}
	if err = writeFileAtomic(trialFile, data); err == nil {
		err = syncFiles()
	}
	if err != nil {
		// Nothing has changed yet: no record is better than one not on the card.
		_ = os.Remove(trialFile)
		return nil, fmt.Errorf("cannot record the current network settings: %w", err)
	}
	active = t
	return t, nil
}

// start arms the revert once the change has been written.
func (t *trial) start() {
	trialMu.Lock()
	defer trialMu.Unlock()
	t.deadline = time.Now().Add(trialDuration)
	t.timer = time.AfterFunc(trialDuration, func() { revertTrial(t, "not confirmed in time", true) })
}

// restarted reports the restart that applies the change: only a successful
// one makes reaching the device a confirmation; a failed one undoes it.
func (t *trial) restarted(err error) {
	if err != nil {
		revertTrial(t, fmt.Sprintf("the interface did not restart: %s", err), true)
		return
	}
	trialMu.Lock()
	defer trialMu.Unlock()
	t.isApplied = true
}

// abandon undoes a trial whose change could not be written completely. The
// interface was not restarted with it, so it is not restarted now either.
func (t *trial) abandon() {
	revertTrial(t, "the change could not be saved", false)
}

// revertTrial restores the previous settings. The trial stays active, so no
// other change can start, until the restore is over.
func revertTrial(t *trial, reason string, restart bool) {
	trialMu.Lock()
	if active != t || t.reverting {
		trialMu.Unlock()
		return
	}
	t.reverting = true
	if t.timer != nil {
		t.timer.Stop()
	}
	trialMu.Unlock()
	finishRevert(t, reason, restart)
}

// finishRevert ends a trial once its previous settings are back and its
// record is gone. Until then the trial keeps blocking other changes, and the
// restore is tried again.
func finishRevert(t *trial, reason string, restart bool) {
	if err := restoreRecord(t.record, reason, restart); err != nil {
		log.Errorf("failed to restore the previous network settings, trying again in %s: %s", trialRetry, err)
		trialMu.Lock()
		if active == t {
			t.retry = time.AfterFunc(trialRetry, func() { finishRevert(t, "a retry", true) })
		}
		trialMu.Unlock()
		return
	}
	trialMu.Lock()
	if active == t {
		active = nil
	}
	trialMu.Unlock()
}

// restoreRecord writes back the previous settings and then removes the
// record: only once both are on the card is the revert done.
func restoreRecord(record trialRecord, reason string, restart bool) error {
	log.Warnf("network change undone (%s): restoring the previous %s settings", reason, record.Service)
	if err := restoreFiles(record.Files); err != nil {
		return err
	}
	if err := syncFiles(); err != nil {
		return fmt.Errorf("sync the previous settings: %w", err)
	}
	// The running network must have them too before the record goes.
	if restart {
		if restartService := trialRestarts[record.Service]; restartService != nil {
			if err := restartService(); err != nil {
				return fmt.Errorf("restart %s with the previous settings: %w", record.Service, err)
			}
		}
	}
	if err := os.Remove(trialFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove the trial record: %w", err)
	}
	if err := syncFiles(); err != nil {
		return fmt.Errorf("sync the removal of the trial record: %w", err)
	}
	return nil
}

var errNoTrial = errors.New("no network change is waiting for this confirmation")

// confirmTrial keeps a change. Its record goes first: while it is there, a
// restart would still undo the change, so the trial and its token stay live
// until the record is gone.
func confirmTrial(token string) error {
	trialMu.Lock()
	defer trialMu.Unlock()
	t := active
	if t == nil || t.timer == nil || !t.isApplied || t.reverting ||
		subtle.ConstantTimeCompare([]byte(token), []byte(t.token)) != 1 {
		return errNoTrial
	}
	if err := os.Remove(trialFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Errorf("failed to remove the network trial record: %s", err)
		return fmt.Errorf("cannot keep the change: %w", err)
	}
	if err := syncFiles(); err != nil {
		// The removal may not be on the card: put the record back, so that
		// a restart and the timer still agree with what was reported.
		if data, marshalErr := json.Marshal(t.record); marshalErr == nil {
			_ = writeFileAtomic(trialFile, data)
		}
		log.Errorf("failed to sync the confirmation: %s", err)
		return fmt.Errorf("cannot keep the change: %w", err)
	}
	t.timer.Stop()
	active = nil
	log.Infof("network change confirmed")
	return nil
}

// RestorePendingTrial undoes a change that was still on trial when the
// server stopped: nobody confirmed it. Until the restore is over, no new
// change starts.
func RestorePendingTrial() {
	data, err := os.ReadFile(trialFile)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	var record trialRecord
	if err == nil {
		err = json.Unmarshal(data, &record)
	}
	if err == nil && record.Files == nil {
		err = errors.New("no files")
	}
	if err == nil {
		for path := range record.Files {
			if !trialPathAllowed(path) {
				err = fmt.Errorf("it names %s", path)
				break
			}
		}
	}
	if err != nil {
		// Kept aside for a look: the unconfirmed change stays in effect.
		log.Errorf("NETWORK CHANGE NOT UNDONE: the trial record %s is unusable (%v); moved to %s.bad", trialFile, err, trialFile)
		_ = os.Rename(trialFile, trialFile+".bad")
		_ = syncFiles()
		return
	}
	t := &trial{record: record, reverting: true}
	trialMu.Lock()
	active = t
	trialMu.Unlock()
	go finishRevert(t, "the device restarted before it was confirmed", true)
}

// Only the settings files a trial can cover are ever restored.
func trialPathAllowed(path string) bool {
	switch path {
	case ethernetConfigFile, ethernetDisabledFile, ethernetVLANFile:
		return true
	}
	return false
}

// trialInfo is what the page that made the change needs to find the device.
type trialInfo struct {
	ID        string   `json:"id"`
	Token     string   `json:"token"`
	ExpiresIn int      `json:"expiresIn"`
	Hosts     []string `json:"hosts"`
}

func (t *trial) info(hosts []string) trialInfo {
	return trialInfo{ID: t.id, Token: t.token, ExpiresIn: int(trialDuration / time.Second), Hosts: hosts}
}

var interfaceAddrs = net.InterfaceAddrs

var mdnsHost = func() string {
	if _, err := os.Stat("/run/avahi-daemon/pid"); err != nil {
		return ""
	}
	name, err := os.ReadFile("/etc/hostname")
	if err != nil {
		return ""
	}
	if host := strings.TrimSpace(string(name)); host != "" {
		return host + ".local"
	}
	return ""
}

// trialHosts lists where the device may be found after a change, the most
// likely first: the new static address, the address the page uses now, the
// mDNS name, then every other IPv4 address the device has now.
func trialHosts(newAddress, requestHost string) []string {
	var hosts []string
	seen := map[string]bool{}
	add := func(host string) {
		if host != "" && !seen[host] {
			seen[host] = true
			hosts = append(hosts, host)
		}
	}
	add(newAddress)
	if host, _, err := net.SplitHostPort(requestHost); err == nil {
		add(strings.Trim(host, "[]"))
	} else {
		add(strings.Trim(requestHost, "[]"))
	}
	add(mdnsHost())
	var others []string
	if addrs, err := interfaceAddrs(); err == nil {
		for _, addr := range addrs {
			prefix, err := netip.ParsePrefix(addr.String())
			if err != nil || !prefix.Addr().Is4() || prefix.Addr().IsLoopback() {
				continue
			}
			others = append(others, prefix.Addr().String())
		}
	}
	sort.Strings(others)
	for _, host := range others {
		add(host)
	}
	return hosts
}

// ProbeTrial tells a page on another address whether this device has its
// trial. Only the trial's public ID is shown, to any origin.
func (s *Service) ProbeTrial(c *gin.Context) {
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Cache-Control", "no-store")
	trialMu.Lock()
	id, applied := "", false
	if active != nil && active.timer != nil && !active.reverting {
		id, applied = active.id, active.isApplied
	}
	trialMu.Unlock()
	var rsp proto.Response
	rsp.OkRspWithData(c, gin.H{"id": id, "applied": applied})
}

// ConfirmTrial keeps a change on trial. The one-time token is the
// authorization: it reaches only the page that made the change, and a page
// on a new address has no login session yet.
func (s *Service) ConfirmTrial(c *gin.Context) {
	var req struct {
		Token string `json:"token"`
	}
	var rsp proto.Response
	if err := c.ShouldBindJSON(&req); err != nil || req.Token == "" {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}
	if err := confirmTrial(req.Token); errors.Is(err, errNoTrial) {
		rsp.ErrRsp(c, -2, err.Error())
		return
	} else if err != nil {
		rsp.ErrRsp(c, -4, err.Error())
		return
	}
	rsp.OkRsp(c)
}
