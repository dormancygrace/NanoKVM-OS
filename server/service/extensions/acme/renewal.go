package acme

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"
)

// Renewal cadence: acme.sh --renew without --force renews only when due (30
// days before expiry; exit code 2 otherwise), so a check twice a day costs
// nothing when nothing is due and a missed check is caught by the next one.
var (
	renewalFirstDelay = 10 * time.Minute
	renewalInterval   = 12 * time.Hour
)

// RunRenewal renews the selected certificate on a schedule while one is set
// up and serves the renewed pair.
func RunRenewal(ctx context.Context) {
	timer := time.NewTimer(renewalFirstDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		renewDue()
		timer.Reset(renewalInterval)
	}
}

// renewDue renews the selected certificate when it is due. Only that one:
// acme.sh --cron would renew every record it knows and reinstall each to the
// shared acme.crt/acme.key. It takes the same admission lock as the
// administrative operations and skips the round when one is in progress.
func renewDue() {
	if !opMu.TryLock() {
		return
	}
	defer opMu.Unlock()
	if !Installed() {
		return
	}
	settings, err := loadSettings()
	if err != nil {
		return
	}
	steps := []step{renewStep(settings, false)}
	err = startJob("cron", nil, nil, steps, func(j *Job) error {
		if !j.OK || j.NotDue {
			return nil
		}
		return reloadIfInUse()
	})
	if err != nil {
		log.Debugf("acme.sh renewal skipped: %v", err)
	}
}

// renewStep renews the issued certificate (force: also before it is due).
// acme.sh reloads the DNS wait saved at issue, so the step carries it for
// the job timeout.
func renewStep(settings *Settings, force bool) step {
	st := step{args: []string{"--renew", "-d", settings.primary(), "--ecc"}, dnsSleep: settings.DNSSleep}
	if force {
		st.args = append(st.args, "--force")
	} else {
		st.okCodes = []int{renewSkip}
	}
	return st
}
