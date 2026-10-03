package picoclaw

import (
	"time"
)

const (
	picoclawBinaryPath      = "/usr/bin/picoclaw"
	picoclawCacheDir        = "/root/.picoclaw-cache"
	picoclawPIDFileName     = ".picoclaw.pid"
	picoclawDownloadURL     = "https://github.com/sipeed/picoclaw/releases/latest/download/picoclaw_Linux_riscv64.tar.gz"
	etcInitPicoclawScript   = "/etc/init.d/S96picoclaw"
	kvmappPicoclawScript    = "/kvmapp/system/init.d/S96picoclaw"
	picoclawStartTimeout    = 15 * time.Second
	picoclawStopTimeout     = 15 * time.Second
	picoclawOnboardTimeout  = 60 * time.Second
	picoclawInstallTimeout  = 12 * time.Minute
	picoclawDownloadTimeout = 10 * time.Minute
	picoclawStartWaitPeriod = 1500 * time.Millisecond
	picoclawStopWaitPeriod  = 500 * time.Millisecond
	picoclawReadyPollPeriod = 500 * time.Millisecond
)
