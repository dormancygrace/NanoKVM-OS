package osupdate

import (
	"regexp"
	"strings"
)

var namedReleaseVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-(alpha|beta|rc)\.(0|[1-9][0-9]*))?$`)

// Tags retain SemVer (v1.0.0-beta.10); displayed releases and assets use beta-10.
func canonicalReleaseAsset(tag string) string {
	version := strings.TrimPrefix(tag, "v")
	if !namedReleaseVersion.MatchString(version) {
		return ""
	}
	core, pre, found := strings.Cut(version, "-")
	if found {
		core += "-" + strings.ReplaceAll(pre, ".", "-")
	}
	return "NanoKVM-OS-v" + core + ".nkos"
}

func releaseAssetMatches(tag, name string) bool {
	if name == "NanoKVM-OS-update.nkos" || name == "NanoKVM-OS-application.nkos" {
		return true
	}
	expected := canonicalReleaseAsset(tag)
	return expected != "" && name == expected
}
