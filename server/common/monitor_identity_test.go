package common

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Tests that do not set an identity must not read the host's /etc/kvm.
func init() { monitorIdentityFile = filepath.Join(os.TempDir(), "nanokvm-test-no-monitor-identity") }

// identityEDID is a profile shaped like the shipped ones: NanoKVM's identity,
// a serial number descriptor at 72 and a monitor name descriptor at 90.
func identityEDID() []byte {
	e := testEDID(1920, 280, 1080, 45, 14850, 16)
	binary.BigEndian.PutUint16(e[8:10], uint16('S'-'@')<<10|uint16('P'-'@')<<5|uint16('D'-'@'))
	e[10], e[11] = 0x01, 0x33
	binary.LittleEndian.PutUint32(e[12:16], 108060)
	e[16], e[17] = 30, 35
	copy(e[72:90], append([]byte{0, 0, 0, 0xff, 0}, []byte("NKVM1080-60\n ")...))
	copy(e[90:108], append([]byte{0, 0, 0, 0xfc, 0}, []byte("NanoKVM\n     ")...))
	copy(e[108:126], []byte{0, 0, 0, 0x10})
	var sum byte
	for _, b := range e[:127] {
		sum += b
	}
	e[127] = -sum
	return e
}

func testIdentity() MonitorIdentity {
	return MonitorIdentity{Manufacturer: "ACR", ProductCode: 0x0bf4, Name: "XB271HU", Week: 12, Year: 2019}
}

func TestWithMonitorIdentityKeepsEachProfileSerial(t *testing.T) {
	in := identityEDID()
	out, err := withMonitorIdentity(in, testIdentity())
	if err != nil {
		t.Fatal(err)
	}
	if !edidValid(out) {
		t.Fatal("checksum not recomputed")
	}
	if got := binary.BigEndian.Uint16(out[8:10]); got != 0x0472 { // ACR
		t.Fatalf("manufacturer %#04x, want 0x0472", got)
	}
	if got := binary.LittleEndian.Uint16(out[10:12]); got != 0x0bf4 {
		t.Fatalf("product code %#04x", got)
	}
	if got := binary.LittleEndian.Uint32(out[12:16]); got != 108060 {
		t.Fatalf("serial %d, want the profile's own 108060", got)
	}
	if out[16] != 12 || out[17] != 29 {
		t.Fatalf("week/year %d/%d", out[16], out[17])
	}
	// The serial text no longer names NanoKVM: the profile's serial in decimal.
	if got := string(out[77:90]); got != "108060\n      " {
		t.Fatalf("serial descriptor %q", got)
	}
	if got := string(out[95:108]); got != "XB271HU\n     " {
		t.Fatalf("name descriptor %q", got)
	}
	if !bytes.Equal(out[54:72], in[54:72]) || !bytes.Equal(out[108:127], in[108:127]) || !bytes.Equal(out[128:], in[128:]) {
		t.Fatal("timings or extension changed")
	}
	if !bytes.Equal(in, identityEDID()) {
		t.Fatal("the profile itself was modified")
	}
}

func TestWithMonitorIdentityFixedSerial(t *testing.T) {
	id := testIdentity()
	serial := uint32(0x01020304)
	id.SerialNumber, id.Name = &serial, "ABCDEFGHIJKLM" // 13 characters: no line feed
	out, err := withMonitorIdentity(identityEDID(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(out[12:16]); got != serial {
		t.Fatalf("serial %#x", got)
	}
	if got := string(out[77:90]); got != "16909060\n    " {
		t.Fatalf("serial descriptor %q", got)
	}
	if got := string(out[95:108]); got != "ABCDEFGHIJKLM" {
		t.Fatalf("name descriptor %q", got)
	}
	id.SerialText = "T7ZAA0024321"
	out, err = withMonitorIdentity(identityEDID(), id)
	if err != nil || string(out[77:90]) != "T7ZAA0024321\n" || !edidValid(out) {
		t.Fatalf("serial text %q, %v", out[77:90], err)
	}
}

func TestMonitorIdentityValidate(t *testing.T) {
	serial := uint32(1)
	for name, change := range map[string]func(*MonitorIdentity){
		"lowercase manufacturer": func(id *MonitorIdentity) { id.Manufacturer = "acr" },
		"short manufacturer":     func(id *MonitorIdentity) { id.Manufacturer = "AC" },
		"empty name":             func(id *MonitorIdentity) { id.Name = "" },
		"long name":              func(id *MonitorIdentity) { id.Name = "ABCDEFGHIJKLMN" },
		"non-ASCII name":         func(id *MonitorIdentity) { id.Name = "Moniteur é" },
		"control in name":        func(id *MonitorIdentity) { id.Name = "A\nB" },
		"text without serial":    func(id *MonitorIdentity) { id.SerialText = "X" },
		"long serial text":       func(id *MonitorIdentity) { id.SerialNumber, id.SerialText = &serial, "ABCDEFGHIJKLMN" },
		"week":                   func(id *MonitorIdentity) { id.Week = 55 },
		"early year":             func(id *MonitorIdentity) { id.Year = 1989 },
		"late year":              func(id *MonitorIdentity) { id.Year = 2246 },
	} {
		id := testIdentity()
		change(&id)
		if id.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
	id := testIdentity()
	id.Week = 255 // model year
	if err := id.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestWithMonitorIdentityNeedsNameDescriptor(t *testing.T) {
	e := testEDID(1920, 280, 1080, 45, 14850, 16)
	if _, err := withMonitorIdentity(e, testIdentity()); err == nil {
		t.Fatal("a profile without a monitor name descriptor was accepted")
	}
}

// isolateMonitorFiles points every monitor state file into a test directory
// holding the 1080p 60 Hz rate profile.
func isolateMonitorFiles(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	oldRes, oldPortrait, oldPortraitRes := monitorResolutionFile, monitorPortraitFile, monitorPortraitResolutionFile
	oldDir, oldHash, oldIdentity, oldFPS := monitorEDIDDir, monitorEDIDHashFile, monitorIdentityFile, GetScreen().FPS
	oldVerified, oldVersion := monitorReceiverVerifiedFile, monitorSystemVersionFile
	t.Cleanup(func() {
		monitorResolutionFile, monitorPortraitFile, monitorPortraitResolutionFile = oldRes, oldPortrait, oldPortraitRes
		monitorEDIDDir, monitorEDIDHashFile, monitorIdentityFile = oldDir, oldHash, oldIdentity
		monitorReceiverVerifiedFile, monitorSystemVersionFile = oldVerified, oldVersion
		SetScreen("fps", oldFPS)
	})
	monitorResolutionFile, monitorPortraitFile = filepath.Join(dir, "resolution"), filepath.Join(dir, "portrait")
	monitorPortraitResolutionFile = filepath.Join(dir, "portrait_resolution")
	monitorEDIDDir, monitorEDIDHashFile = dir, filepath.Join(dir, "hash")
	monitorIdentityFile = filepath.Join(dir, "identity")
	monitorReceiverVerifiedFile, monitorSystemVersionFile = filepath.Join(dir, "verified"), filepath.Join(dir, "version")
	SetScreen("fps", 60)
	if err := os.WriteFile(monitorResolutionFile, []byte("1080"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "NanoKVM-monitor-1080-60.bin"), identityEDID(), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestMonitorIdentitySavedOnlyAfterWrite(t *testing.T) {
	selectBoard(t, "pcie", "ux")
	isolateMonitorFiles(t)
	id := testIdentity()
	var applied []*MonitorIdentity
	fail := errors.New("receiver error")
	hw := monitorSettingsBackend{
		requireHardware: func() error { return nil }, portraitSupported: func(uint16) bool { return true },
		powerCycle: func() bool { return false }, followsRefresh: func() bool { return true },
		apply: func(path string, got *MonitorIdentity) error {
			applied = append(applied, got)
			if filepath.Base(path) != "NanoKVM-monitor-1080-60.bin" {
				t.Fatalf("wrote %s", path)
			}
			return fail
		},
	}
	update := MonitorSettings{SetIdentity: true, Identity: &id}
	if err := applyMonitorSettingsLocked(update, hw); err == nil {
		t.Fatal("write error not returned")
	}
	if savedMonitorIdentityLocked() != nil {
		t.Fatal("identity saved although the write failed")
	}
	fail = nil
	if err := applyMonitorSettingsLocked(update, hw); err != nil {
		t.Fatal(err)
	}
	if len(applied) != 2 || applied[1] == nil || *applied[1] != id {
		t.Fatalf("applied %v", applied)
	}
	if saved := savedMonitorIdentityLocked(); saved == nil || *saved != id {
		t.Fatalf("saved %v", saved)
	}
	// An FPS change keeps the saved identity.
	if err := applyMonitorSettingsLocked(MonitorSettings{SyncRefresh: true}, hw); err != nil {
		t.Fatal(err)
	}
	if last := applied[len(applied)-1]; last == nil || *last != id {
		t.Fatalf("refresh wrote identity %v", last)
	}
	// Restoring NanoKVM's own writes the profile as shipped and clears the file.
	if err := applyMonitorSettingsLocked(MonitorSettings{SetIdentity: true}, hw); err != nil {
		t.Fatal(err)
	}
	if applied[len(applied)-1] != nil {
		t.Fatal("restoring wrote an identity")
	}
	if _, err := os.Stat(monitorIdentityFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("identity file left: %v", err)
	}
	bad := testIdentity()
	bad.Manufacturer = "x"
	before := len(applied)
	if err := applyMonitorSettingsLocked(MonitorSettings{SetIdentity: true, Identity: &bad}, hw); err == nil || len(applied) != before {
		t.Fatal("an invalid identity was written")
	}
}

func TestProgrammedProfileWithIdentity(t *testing.T) {
	selectBoard(t, "pcie", "ux")
	dir := isolateMonitorFiles(t)
	path := filepath.Join(dir, "NanoKVM-monitor-1080-60.bin")
	id := testIdentity()
	if err := persistMonitorIdentityLocked(&id); err != nil {
		t.Fatal(err)
	}
	written, err := monitorProfileBytes(path, &id)
	if err != nil {
		t.Fatal(err)
	}
	recordMonitorProfileLocked(written)
	if !programmedMonitorProfileLocked(path, 1080) {
		t.Fatal("the profile written with the saved identity is not recognised")
	}
	if MonitorRefreshHz() != 60 {
		t.Fatal("refresh rate of the profile with identity not found")
	}
	if shippedMonitorProfileLocked(edidHash(written)) != path {
		t.Fatal("shipped profile with identity not found by hash")
	}
	if err := persistMonitorIdentityLocked(nil); err != nil {
		t.Fatal(err)
	}
	if programmedMonitorProfileLocked(path, 1080) {
		t.Fatal("the shipped profile was taken for the one written with an identity")
	}
}
