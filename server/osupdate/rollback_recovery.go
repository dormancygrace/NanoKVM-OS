package osupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"NanoKVM-Server/internal/apkrollback"
	"NanoKVM-Server/internal/apkrun"
)

var rollbackRecoveryRoot = "/"

func recoveryPaths() (string, string) {
	return filepath.Join(rollbackRecoveryRoot, "etc/init.d", apkrollback.Service),
		filepath.Join(rollbackRecoveryRoot, "etc/runlevels/boot", apkrollback.Service)
}

func syncPath(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func installRollbackRecovery() error {
	script, link := recoveryPaths()
	// Real Alpine already has both directories; tests use a disposable root.
	for _, dir := range []string{filepath.Dir(script), filepath.Dir(link)} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	if err := writeAtomic(script, apkrollback.RecoveryService, 0755); err != nil {
		return err
	}
	if err := syncPath(script); err != nil {
		return err
	}
	if err := os.Symlink(script, link); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		if target, e := os.Readlink(link); e != nil || target != script {
			return errors.New("unexpected rollback recovery service link")
		}
	}
	// Both the script and boot registration must be durable before APK replaces
	// the updater. A defer or a new-server startup hook cannot survive a downgrade.
	for _, dir := range []string{filepath.Dir(script), filepath.Dir(link)} {
		if err := syncPath(dir); err != nil {
			return err
		}
	}
	return nil
}

func removeRollbackRecovery() error {
	script, link := recoveryPaths()
	for _, path := range []string{link, script} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Notice checks can recover without a reboot while the new helper is still
// installed. Never delete a constraint belonging to an active rollback.
func recoverInterruptedRollback(ctx context.Context) error {
	if !rollbackConstraintInstalled() {
		return nil
	}
	lock, err := updateLock()
	if err != nil {
		return err
	}
	defer lock.Close()
	if !rollbackConstraintInstalled() {
		return nil
	}
	if err := apkrun.Command(ctx, apkExecutable, "--no-network", "del", rollbackConstraint).Run(); err != nil {
		return err
	}
	return removeRollbackRecovery()
}
