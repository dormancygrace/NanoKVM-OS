package common

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"

	log "github.com/sirupsen/logrus"
)

// MonitorIdentity is the identity the monitor profiles carry instead of
// NanoKVM's own (manufacturer SPD, monitor name and serial text NanoKVM):
// the fields a computer shows and keys its per-monitor settings to. The modes
// stay those of the profile.
type MonitorIdentity struct {
	// Manufacturer is the three-letter PNP ID, such as ACR or DEL.
	Manufacturer string `json:"manufacturer"`
	ProductCode  uint16 `json:"productCode"`
	// Name is the monitor name descriptor, up to 13 printable ASCII characters.
	Name string `json:"name"`
	// SerialNumber is the numeric serial of every profile. Nil keeps each
	// profile's own: Windows keeps the display mode it last used per monitor
	// identity, and each rate profile has its own serial so that Windows meets
	// it as a new monitor and starts from its preferred timing.
	SerialNumber *uint32 `json:"serialNumber"`
	// SerialText is the serial number descriptor, used with SerialNumber; when
	// empty it is SerialNumber in decimal. Without SerialNumber the descriptor
	// is each profile's own numeric serial in decimal.
	SerialText string `json:"serialText"`
	// Week is the week of manufacture, 0 when unknown, 255 when Year is the
	// model year.
	Week uint8 `json:"week"`
	// Year is the year of manufacture (or model year), 1990-2245.
	Year uint16 `json:"year"`
}

// monitorIdentityFile holds the saved identity as JSON; absent means NanoKVM's own.
var monitorIdentityFile = "/etc/kvm/monitor_identity"

// The written profile when an identity is set: nanokvm_update_edid takes a file.
var monitorIdentityProfileFile = "/run/nanokvm-monitor-edid.bin"

const edidTextLength = 13

func edidText(s string) bool {
	if len(s) > edidTextLength {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// Validate checks every field against what EDID can carry.
func (id MonitorIdentity) Validate() error {
	if len(id.Manufacturer) != 3 {
		return errors.New("the manufacturer ID must be three letters A-Z")
	}
	for i := 0; i < 3; i++ {
		if id.Manufacturer[i] < 'A' || id.Manufacturer[i] > 'Z' {
			return errors.New("the manufacturer ID must be three letters A-Z")
		}
	}
	if id.Name == "" || !edidText(id.Name) {
		return errors.New("the monitor name must be 1-13 printable ASCII characters")
	}
	if !edidText(id.SerialText) {
		return errors.New("the serial number text must be at most 13 printable ASCII characters")
	}
	if id.SerialText != "" && id.SerialNumber == nil {
		return errors.New("the serial number text needs a serial number")
	}
	if id.Week > 54 && id.Week != 255 {
		return errors.New("the week of manufacture must be 0-54, or 255 for a model year")
	}
	if id.Year < 1990 || id.Year > 2245 {
		return errors.New("the year must be 1990-2245")
	}
	return nil
}

// edidTextDescriptor fills the 13 text bytes of a display descriptor: the
// text, then a line feed and spaces when it is shorter.
func edidTextDescriptor(dst []byte, text string) {
	n := copy(dst, text)
	if n < len(dst) {
		dst[n] = '\n'
		for i := n + 1; i < len(dst); i++ {
			dst[i] = ' '
		}
	}
}

// withMonitorIdentity returns a copy of a profile carrying the identity:
// manufacturer, product code, serial number and date in the base block, and
// the monitor name and serial number descriptors; the checksum is
// recomputed. Every shipped profile has both descriptors.
func withMonitorIdentity(edid []byte, id MonitorIdentity) ([]byte, error) {
	if err := id.Validate(); err != nil {
		return nil, err
	}
	if len(edid) < 128 || string(edid[:8]) != "\x00\xff\xff\xff\xff\xff\xff\x00" {
		return nil, errors.New("the monitor profile is not an EDID")
	}
	out := append([]byte(nil), edid...)
	m := id.Manufacturer
	binary.BigEndian.PutUint16(out[8:10], uint16(m[0]-'@')<<10|uint16(m[1]-'@')<<5|uint16(m[2]-'@'))
	binary.LittleEndian.PutUint16(out[10:12], id.ProductCode)
	serial := binary.LittleEndian.Uint32(out[12:16])
	text := strconv.FormatUint(uint64(serial), 10)
	if id.SerialNumber != nil {
		serial = *id.SerialNumber
		binary.LittleEndian.PutUint32(out[12:16], serial)
		text = id.SerialText
		if text == "" {
			text = strconv.FormatUint(uint64(serial), 10)
		}
	}
	out[16], out[17] = id.Week, byte(id.Year-1990)
	named := false
	for pos := 54; pos+18 <= 126; pos += 18 {
		if out[pos] != 0 || out[pos+1] != 0 || out[pos+2] != 0 {
			continue // a detailed timing
		}
		switch out[pos+3] {
		case 0xfc:
			edidTextDescriptor(out[pos+5:pos+18], id.Name)
			named = true
		case 0xff:
			edidTextDescriptor(out[pos+5:pos+18], text)
		}
	}
	if !named {
		return nil, errors.New("the monitor profile has no monitor name descriptor")
	}
	var sum byte
	for _, b := range out[:127] {
		sum += b
	}
	out[127] = -sum
	return out, nil
}

// savedMonitorIdentityLocked is the saved identity, nil for NanoKVM's own. A
// file that does not parse or validate counts as none.
func savedMonitorIdentityLocked() *MonitorIdentity {
	data, err := os.ReadFile(monitorIdentityFile)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Warnf("cannot read the monitor identity: %v", err)
		}
		return nil
	}
	var id MonitorIdentity
	if err := json.Unmarshal(data, &id); err != nil {
		log.Warnf("ignoring the saved monitor identity: %v", err)
		return nil
	}
	if err := id.Validate(); err != nil {
		log.Warnf("ignoring the saved monitor identity: %v", err)
		return nil
	}
	return &id
}

// SavedMonitorIdentity is the identity the monitor profiles carry, nil for
// NanoKVM's own.
func SavedMonitorIdentity() *MonitorIdentity {
	monitorMutex.Lock()
	defer monitorMutex.Unlock()
	return savedMonitorIdentityLocked()
}

func persistMonitorIdentityLocked(id *MonitorIdentity) error {
	if id == nil {
		if err := os.Remove(monitorIdentityFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("clearing the monitor identity failed: %w", err)
		}
		return nil
	}
	data, err := json.Marshal(id)
	if err != nil {
		return err
	}
	if err := atomicWriteMonitorFile(monitorIdentityFile, append(data, '\n'), ".monitor_identity-*"); err != nil {
		return fmt.Errorf("saving the monitor identity failed: %w", err)
	}
	return nil
}

// monitorProfileBytes is a profile as it is written: the file itself, or
// with the identity.
func monitorProfileBytes(path string, id *MonitorIdentity) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil || id == nil {
		return data, err
	}
	return withMonitorIdentity(data, *id)
}

// monitorProfileSHA256 is the hash recorded after writing a profile with
// this identity, so that a profile is recognised as the one programmed.
func monitorProfileSHA256(path string, id *MonitorIdentity) (string, error) {
	data, err := monitorProfileBytes(path, id)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
