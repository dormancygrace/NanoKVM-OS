package vm

import (
	"NanoKVM-Server/internal/oomscore"
	"NanoKVM-Server/proto"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

// Bluetooth on the AIC8800 is the aic8800_btsdio module. It is off unless
// this file asks modprobe to load it after the Wi-Fi module, at boot and on
// every later load; without it, bluetooth.ko is never loaded.
const bluetoothConfMarker = "# Managed by NanoKVM: Bluetooth setting. Remove to turn Bluetooth off.\n"

var (
	bluetoothConf      = "/etc/modprobe.d/nanokvm-bluetooth.conf"
	bluetoothSysModule = "/sys/module"
	bluetoothModules   = "/lib/modules"
	bluetoothHci       = "/sys/class/bluetooth/hci0"
	bluetoothRun       = runBluetoothCommand
	bluetoothVersion   = readBluetoothVersion
	bluetoothRelease   = kernelRelease
	bluetoothMu        sync.Mutex

	bluetoothReadyTimeout = 5 * time.Second
)

const (
	bluetoothModule  = "aic8800_btsdio"
	bluetoothWifiMod = "aic8800_fdrv"
)

func (s *Service) GetBluetoothState(c *gin.Context) {
	var rsp proto.Response
	bluetoothMu.Lock()
	state := bluetoothState()
	bluetoothMu.Unlock()
	rsp.OkRspWithData(c, &state)
}

func (s *Service) EnableBluetooth(c *gin.Context) {
	setBluetooth(c, true)
}

func (s *Service) DisableBluetooth(c *gin.Context) {
	setBluetooth(c, false)
}

func setBluetooth(c *gin.Context, enabled bool) {
	var rsp proto.Response
	bluetoothMu.Lock()
	defer bluetoothMu.Unlock()
	if !bluetoothAvailable() {
		rsp.ErrRsp(c, -2, "Bluetooth is not available on this device")
		return
	}
	if err := applyBluetooth(enabled); err != nil {
		log.Errorf("failed to turn Bluetooth %s: %s", map[bool]string{true: "on", false: "off"}[enabled], err)
		rsp.ErrRsp(c, -1, "failed to change Bluetooth state")
		return
	}
	rsp.OkRsp(c)
}

func bluetoothState() proto.GetBluetoothStateRsp {
	state := proto.GetBluetoothStateRsp{
		Available: bluetoothAvailable(),
		Enabled:   bluetoothConfOwned(),
		Loaded:    moduleLoaded(bluetoothModule),
	}
	// Only what the controller itself reports, so only while it is up.
	if state.Loaded {
		if version, err := bluetoothVersion(); err == nil {
			state.Version = version
		} else {
			log.Debugf("Bluetooth version: %s", err)
		}
	}
	return state
}

// The setting exists where the AIC Wi-Fi driver runs and the image ships the
// HCI module (not on boards with another Wi-Fi chip or without Wi-Fi).
func bluetoothAvailable() bool {
	if !moduleLoaded(bluetoothWifiMod) {
		return false
	}
	release, err := bluetoothRelease()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(bluetoothModules, release, "extra", bluetoothModule+".ko"))
	return err == nil
}

func moduleLoaded(name string) bool {
	_, err := os.Stat(filepath.Join(bluetoothSysModule, name))
	return err == nil
}

func bluetoothConfOwned() bool {
	data, err := os.ReadFile(bluetoothConf)
	return err == nil && strings.HasPrefix(string(data), bluetoothConfMarker)
}

func applyBluetooth(enabled bool) error {
	if enabled {
		if err := writeBluetoothConf(); err != nil {
			return err
		}
		if moduleLoaded(bluetoothModule) {
			return nil
		}
		if err := bluetoothRun("modprobe", bluetoothModule); err != nil {
			return err
		}
		// hci0 answers management commands only after its HCI init; wait for
		// it so that the state the page reads next has the version.
		deadline := time.Now().Add(bluetoothReadyTimeout)
		for {
			if _, err := bluetoothVersion(); err == nil || time.Now().After(deadline) {
				return nil
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
	if err := removeBluetoothConf(); err != nil {
		return err
	}
	if moduleLoaded(bluetoothModule) {
		// Not "modprobe -r": it would also remove aic8800_fdrv, which the
		// HCI module leaves unused, and with it Wi-Fi.
		if err := bluetoothRun("rmmod", bluetoothModule); err != nil {
			return err
		}
	}
	if moduleLoaded("bluetooth") {
		// Best effort: something else may still use the Bluetooth stack.
		if err := bluetoothRun("modprobe", "-r", "bluetooth"); err != nil {
			log.Infof("bluetooth.ko stays loaded: %s", err)
		}
	}
	return nil
}

func writeBluetoothConf() error {
	if data, err := os.ReadFile(bluetoothConf); err == nil && !strings.HasPrefix(string(data), bluetoothConfMarker) {
		return fmt.Errorf("%s exists and is not managed by NanoKVM", bluetoothConf)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	content := bluetoothConfMarker + "softdep " + bluetoothWifiMod + " post: " + bluetoothModule + "\n"
	dir := filepath.Dir(bluetoothConf)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".nanokvm-bluetooth-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.WriteString(content); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0644)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), bluetoothConf)
	}
	return err
}

func removeBluetoothConf() error {
	data, err := os.ReadFile(bluetoothConf)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.HasPrefix(string(data), bluetoothConfMarker) {
		return fmt.Errorf("%s exists and is not managed by NanoKVM", bluetoothConf)
	}
	return os.Remove(bluetoothConf)
}

func runBluetoothCommand(name string, args ...string) error {
	argv := oomscore.Unprotected(name, args...)
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func kernelRelease() (string, error) {
	var uts unix.Utsname
	if err := unix.Uname(&uts); err != nil {
		return "", err
	}
	return unix.ByteSliceToString(uts.Release[:]), nil
}

// Core specification versions by the HCI/LMP version number.
var bluetoothCoreVersions = []string{
	"1.0b", "1.1", "1.2", "2.0", "2.1", "3.0", "4.0", "4.1", "4.2", "5.0", "5.1", "5.2", "5.3", "5.4", "6.0", "6.1",
}

func bluetoothCoreVersion(hciVersion uint8) string {
	if int(hciVersion) < len(bluetoothCoreVersions) {
		return bluetoothCoreVersions[hciVersion]
	}
	return fmt.Sprintf("HCI %d", hciVersion)
}

// The controller's version, from the kernel's management interface
// (MGMT_OP_READ_INFO); no BlueZ needed.
func readBluetoothVersion() (string, error) {
	index, err := bluetoothIndex()
	if err != nil {
		return "", err
	}
	fd, err := unix.Socket(unix.AF_BLUETOOTH, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.BTPROTO_HCI)
	if err != nil {
		return "", err
	}
	defer unix.Close(fd)
	if err := unix.Bind(fd, &unix.SockaddrHCI{Dev: 0xffff, Channel: unix.HCI_CHANNEL_CONTROL}); err != nil {
		return "", err
	}
	timeout := unix.NsecToTimeval((2 * time.Second).Nanoseconds())
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &timeout); err != nil {
		return "", err
	}
	const readInfo = 0x0004
	req := make([]byte, 6)
	binary.LittleEndian.PutUint16(req[0:], readInfo)
	binary.LittleEndian.PutUint16(req[2:], index)
	if _, err := unix.Write(fd, req); err != nil {
		return "", err
	}
	buf := make([]byte, 512)
	for {
		n, err := unix.Read(fd, buf)
		if err != nil {
			return "", err
		}
		version, done, err := parseBluetoothReadInfo(buf[:n], index)
		if done {
			return version, err
		}
	}
}

// One mgmt event: done is false for events that are not our reply.
func parseBluetoothReadInfo(ev []byte, index uint16) (version string, done bool, err error) {
	const cmdComplete, cmdStatus, readInfo = 0x0001, 0x0002, 0x0004
	if len(ev) < 6 || binary.LittleEndian.Uint16(ev[2:]) != index {
		return "", false, nil
	}
	code := binary.LittleEndian.Uint16(ev[0:])
	params := ev[6:]
	if code != cmdComplete && code != cmdStatus || len(params) < 3 || binary.LittleEndian.Uint16(params[0:]) != readInfo {
		return "", false, nil
	}
	if params[2] != 0 {
		return "", true, fmt.Errorf("read info: status %d", params[2])
	}
	// Reply: opcode, status, then bdaddr[6], version, manufacturer, ...
	if code != cmdComplete || len(params) < 3+6+1 {
		return "", true, errors.New("read info: short reply")
	}
	return bluetoothCoreVersion(params[3+6]), true, nil
}

// The AIC8800 registers the only controller, hci0.
func bluetoothIndex() (uint16, error) {
	if _, err := os.Stat(bluetoothHci); err != nil {
		return 0, err
	}
	return 0, nil
}
