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
	steps := []step{{args: []string{"--renew", "-d", settings.primary(), "--ecc"}, okCodes: []int{renewSkip}}}
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
