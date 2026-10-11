package acme

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"
)

// Renewal cadence: acme.sh --cron renews only what is due (30 days before
// expiry), so a check twice a day costs nothing when nothing is due and a
// missed check is caught by the next one.
var (
	renewalFirstDelay = 10 * time.Minute
	renewalInterval   = 12 * time.Hour
)

// RunRenewal runs acme.sh --cron on a schedule while a certificate is set up
// and serves the renewed pair.
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

// renewDue starts the cron job when acme.sh is installed with a certificate
// and nothing else is running.
func renewDue() {
	if !Installed() {
		return
	}
	if _, err := loadSettings(); err != nil {
		return
	}
	err := startJob("cron", nil, nil, []step{{args: []string{"--cron"}, okCodes: []int{renewSkip}}}, func(j *Job) {
		if j.OK {
			reloadIfInUse()
		}
	})
	if err != nil {
		log.Debugf("acme.sh renewal skipped: %v", err)
	}
}
