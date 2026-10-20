package common

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// codecFiles points the device codec files at a temporary directory; an empty
// value leaves a file absent.
func codecFiles(t *testing.T, encoder, streamType string) {
	t.Helper()
	dir := t.TempDir()
	oldEncoder, oldType := encoderCodecFile, streamTypeFile
	t.Cleanup(func() { encoderCodecFile, streamTypeFile = oldEncoder, oldType })
	encoderCodecFile, streamTypeFile = filepath.Join(dir, "encoder_codec"), filepath.Join(dir, "type")
	for path, value := range map[string]string{encoderCodecFile: encoder, streamTypeFile: streamType} {
		if value == "" {
			continue
		}
		if err := os.WriteFile(path, []byte(value+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRetiredPortraitResolutionMigrates(t *testing.T) {
	for _, tc := range []struct {
		name              string
		encoder, typ      string
		portrait          bool
		want              uint16
		deferredRequested bool
	}{
		{"H.265 device", "h265", "h265", true, 2560, true},
		{"no codec saved", "", "", true, 2560, true},
		{"H.264 device", "h264", "h264", true, 1920, true},
		{"H.264 by the screen setting only", "", "h264", true, 1920, true},
		{"the encoder selection wins", "h265", "h264", true, 2560, true},
		{"landscape: no EDID request", "h264", "", false, 1920, false},
	} {
		monitorState(t, 1080, tc.portrait, 2304)
		codecFiles(t, tc.encoder, tc.typ)
		// Read before the migration, the setting is already valid.
		if got := PortraitResolution(); got != tc.want {
			t.Errorf("%s: read as %d before the migration, want %d", tc.name, got, tc.want)
		}
		changed, err := MigrateRetiredPortraitResolution()
		if err != nil || !changed {
			t.Fatalf("%s: changed %v, %v", tc.name, changed, err)
		}
		data, _ := os.ReadFile(monitorPortraitResolutionFile)
		if strings.TrimSpace(string(data)) != map[uint16]string{1920: "1920", 2560: "2560"}[tc.want] {
			t.Errorf("%s: saved %q, want %d", tc.name, data, tc.want)
		}
		_, err = os.Stat(monitorDeferredFile)
		if (err == nil) != tc.deferredRequested {
			t.Errorf("%s: deferred request %v, want %v", tc.name, err == nil, tc.deferredRequested)
		}
		// Once only.
		if changed, err := MigrateRetiredPortraitResolution(); changed || err != nil {
			t.Errorf("%s: second run changed %v, %v", tc.name, changed, err)
		}
	}
}

func TestRetiredPortraitResolutionIsProgrammedOnTheFirstStart(t *testing.T) {
	monitorState(t, 1080, true, 2304)
	codecFiles(t, "h265", "")
	// The receiver holds a recorded profile: without the request nothing would
	// be programmed.
	if err := os.WriteFile(monitorEDIDHashFile, []byte("recorded\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateRetiredPortraitResolution(); err != nil {
		t.Fatal(err)
	}
	var applied []string
	if _, err := programMonitorProfileOnFirstStartLocked(firstStartBackend(true, false, &applied, nil), poolUHD); err != nil {
		t.Fatal(err)
	}
	if len(applied) != 1 || applied[0] != portraitProfilePathAt(2560, GetScreen().FPS) {
		t.Fatalf("applied %v", applied)
	}
	if _, err := os.Stat(monitorDeferredFile); err == nil {
		t.Fatal("the request was not cleared")
	}
}

func TestOtherPortraitResolutionsAreNotMigrated(t *testing.T) {
	for _, resolution := range []int{1280, 1920, 2560} {
		monitorState(t, 1080, true, resolution)
		codecFiles(t, "h264", "")
		if changed, err := MigrateRetiredPortraitResolution(); changed || err != nil {
			t.Errorf("%d: changed %v, %v", resolution, changed, err)
		}
		if _, err := os.Stat(monitorDeferredFile); err == nil {
			t.Errorf("%d: deferred request written", resolution)
		}
	}
	if validPortraitResolution(2304) || PortraitWidth(2304) != 0 || len(PortraitRates(2304)) != 0 {
		t.Error("the 1296x2304 portrait profile is still offered")
	}
}

func TestRetiredPortraitRetriesAfterDeferredWriteFailure(t *testing.T) {
	monitorState(t, 1080, true, 2304)
	codecFiles(t, "h264", "")
	// A directory at the destination makes the atomic rename fail.
	if err := os.Mkdir(monitorDeferredFile, 0700); err != nil {
		t.Fatal(err)
	}
	if changed, err := MigrateRetiredPortraitResolution(); changed || err == nil {
		t.Fatalf("failed request: changed %v, error %v", changed, err)
	}
	if got := ReadVideoValue(monitorPortraitResolutionFile); got != 2304 {
		t.Fatalf("lost retry marker: saved %d", got)
	}
	if err := os.Remove(monitorDeferredFile); err != nil {
		t.Fatal(err)
	}
	if changed, err := MigrateRetiredPortraitResolution(); !changed || err != nil {
		t.Fatalf("retry: changed %v, error %v", changed, err)
	}
	var applied []string
	if _, err := programMonitorProfileOnFirstStartLocked(firstStartBackend(true, false, &applied, nil), poolUHD); err != nil {
		t.Fatal(err)
	}
	if len(applied) != 1 || applied[0] != portraitProfilePathAt(1920, GetScreen().FPS) {
		t.Fatalf("applied %v", applied)
	}
}
