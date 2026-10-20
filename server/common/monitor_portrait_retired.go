package common

import (
	"fmt"
	"os"
	"strings"

	log "github.com/sirupsen/logrus"
)

// retiredPortraitResolution is the 1296x2304 portrait profile of earlier
// releases, the tallest portrait H.264 encoded. H.264 now stops at 1080x1920
// and 1440x2560 covers H.265, so the profile is no longer offered or shipped.
const retiredPortraitResolution uint16 = 2304

// The device codec, as the stream service and the screen settings save it.
var (
	encoderCodecFile = "/etc/kvm/encoder_codec"
	streamTypeFile   = "/kvmapp/kvm/type"
)

// retiredPortraitReplacement is the profile a saved 1296x2304 portrait
// becomes: 1080x1920 for a device that encodes H.264, which cannot encode a
// taller portrait, else 1440x2560.
func retiredPortraitReplacement() uint16 {
	codec := ""
	for _, path := range []string{encoderCodecFile, streamTypeFile} {
		if data, err := os.ReadFile(path); err == nil {
			if codec = strings.ToLower(strings.TrimSpace(string(data))); codec != "" {
				break
			}
		}
	}
	if codec == "h264" {
		return portraitResolutionDefault
	}
	return portraitResolutionMax
}

// MigrateRetiredPortraitResolution rewrites a saved 1296x2304 portrait
// resolution once (retiredPortraitReplacement). When portrait is on, the
// receiver still advertises 1296x2304, which capture no longer takes: the
// replacement is saved as a deferred monitor request, so the first-start step
// (ProgramMonitorProfileOnFirstStart) programs it like a profile deferred by a
// video memory switch. It reports whether the setting was rewritten.
func MigrateRetiredPortraitResolution() (bool, error) {
	monitorMutex.Lock()
	defer monitorMutex.Unlock()
	if ReadVideoValue(monitorPortraitResolutionFile) != int(retiredPortraitResolution) {
		return false, nil
	}
	replacement := retiredPortraitReplacement()
	if err := persistMonitorPortraitResolutionLocked(replacement); err != nil {
		return false, fmt.Errorf("saving the portrait profile: %w", err)
	}
	log.Infof("the 1296x2304 portrait profile is retired; the portrait profile is now %dx%d",
		portraitWidths[replacement], replacement)
	if monitorPortraitEnabledLocked() {
		if err := atomicWriteMonitorFile(monitorDeferredFile, []byte("1\n"), ".monitor_edid_deferred-*"); err != nil {
			return true, fmt.Errorf("requesting the portrait profile: %w", err)
		}
	}
	return true, nil
}
