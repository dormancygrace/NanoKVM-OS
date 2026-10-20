// Package apkrollback defines the APK transaction shared by the updater and
// the signed-package integration test.
package apkrollback

import _ "embed"

const Constraint = ".nanokvm-stable-rollback"
const Service = "nanokvm-rollback-recovery"

//go:embed recovery.initd
var RecoveryService []byte

var CorePackages = []string{"nanokvm", "nanokvm-web", "nanokvm-app", "nanokvm-release",
	"nanokvm-base", "nanokvm-keys", "nanokvm-kernel-sg2002", "nanokvm-kmod-sg2002",
	"nanokvm-firmware-sg2002", "nanokvm-bootloader-sg2002", "nanokvm-zlib",
	"nanokvm-exfatprogs", "nanokvm-i2c-tools"}

func Query(repo string) []string {
	// Do not use --no-network: APK 3 can have a differently named cached index
	// after changing repository options. The small index is signature-verified.
	return append([]string{"--repositories-file=/dev/null", "--repository=" + repo, "search", "--exact"}, CorePackages...)
}

func Install(targets []string) []string {
	return append([]string{"add", "--virtual", Constraint}, targets...)
}
