package acme

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"
)

// A job is one acme.sh run (issue, renew, cron) kept for the status page.
type Job struct {
	Action     string `json:"action"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt,omitempty"`
	OK         bool   `json:"ok"`
	// NotDue: acme.sh found nothing to renew (its exit code 2).
	NotDue bool   `json:"notDue,omitempty"`
	Error  string `json:"error,omitempty"`
	// Output is the tail of what acme.sh printed, credentials replaced.
	Output string `json:"output,omitempty"`
}

const (
	jobTimeout = 20 * time.Minute
	// maxDNSSleep is the longest DNS wait the settings accept, in seconds.
	maxDNSSleep = 3600
	outputLimit = 64 * 1024
	renewSkip   = 2
)

var (
	jobMu     sync.Mutex
	jobActive bool
	lastJob   *Job
	// The log file acme.sh appends to (--log), separate from Output.
	logName = "last-run.log"
)

func running() bool {
	jobMu.Lock()
	defer jobMu.Unlock()
	return jobActive
}

func lastRun() *Job {
	jobMu.Lock()
	defer jobMu.Unlock()
	if lastJob == nil {
		return nil
	}
	j := *lastJob
	return &j
}

// tailBuffer keeps the last outputLimit bytes.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > outputLimit {
		b.buf = append([]byte("…"), b.buf[len(b.buf)-outputLimit:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// scrub replaces every secret in the output.
func scrub(text string, secrets []string) string {
	for _, s := range secrets {
		if len(s) >= 4 {
			text = strings.ReplaceAll(text, s, "***")
		}
	}
	return text
}

// baseArgs addresses the installed script and the data directory.
func baseArgs() []string {
	return []string{"--home", binDir(), "--config-home", dataDir(), "--log", filepath.Join(dataDir(), logName)}
}

// command runs the installed acme.sh directly (no shell between: every
// value is one argv entry) with an explicit environment: only PATH, HOME and
// the acme.sh directories plus the provider variables. What it writes stays
// private through the directories: data/ and the TLS directory are 0700,
// and acme.sh makes account.conf and the keys 0600 itself.
func command(ctx context.Context, env []string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, script(), args...)
	// acme.sh runs curl, openssl and the DNS hooks: on a timeout the whole
	// process group goes, or its children would keep Wait from returning.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	cmd.Dir = dataDir()
	cmd.Env = append([]string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=" + dataDir(),
		"LE_WORKING_DIR=" + binDir(),
		"LE_CONFIG_HOME=" + dataDir(),
	}, env...)
	return cmd
}

// step is one acme.sh invocation inside a job.
type step struct {
	args []string
	// okCodes are exit codes that end the step without failing the job.
	okCodes []int
	// dnsSleep is the DNS wait acme.sh spends in this step without a
	// --dnssleep argument: a renewal reloads the wait saved at issue.
	dnsSleep int
}

// startJob runs the steps in the background, one job at a time, then calls
// after with the result. It returns at once; the page polls the status.
// after runs once the steps are done, also when they failed; an error it
// returns fails the job, so "done" means the certificate is in use.
func startJob(action string, env, secrets []string, steps []step, after func(j *Job) error) error {
	jobMu.Lock()
	if jobActive {
		jobMu.Unlock()
		return errors.New("an acme.sh operation is already running")
	}
	jobActive = true
	job := &Job{Action: action, StartedAt: time.Now().UTC().Format(time.RFC3339)}
	lastJob = job
	jobMu.Unlock()

	go func() {
		result := runSteps(job, env, secrets, steps)
		// The follow-up (serving the new pair) belongs to the job: the
		// page sees it finished only once the certificate is in use.
		if after != nil {
			if err := after(result); err != nil {
				log.Errorf("acme.sh %s: %v", action, err)
				result.OK = false
				result.NotDue = false
				result.Error = err.Error()
			}
		}
		jobMu.Lock()
		lastJob = result
		jobActive = false
		jobMu.Unlock()
	}()
	return nil
}

func runSteps(job *Job, env, secrets []string, steps []step) *Job {
	timeout := stepsTimeout(steps)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// The log holds the last run only: the renewal check runs twice a day,
	// and appending would grow the file on the SD card forever.
	removeLog()
	out := &tailBuffer{}
	result := *job
	for _, st := range steps {
		code, err := runStep(ctx, out, env, st)
		if err == nil {
			if code == renewSkip {
				result.NotDue = true
			}
			continue
		}
		result.Error = err.Error()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			result.Error = "acme.sh did not finish within " + timeout.String()
		}
		result.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		result.Output = scrub(out.String(), secrets)
		log.Errorf("acme.sh %s failed: %s", job.Action, result.Error)
		return &result
	}
	result.OK = true
	result.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	result.Output = scrub(out.String(), secrets)
	log.Infof("acme.sh %s finished", job.Action)
	return &result
}

func runStep(ctx context.Context, out *tailBuffer, env []string, st step) (int, error) {
	if err := ensurePrivateDirs(); err != nil {
		return -1, err
	}
	cmd := command(ctx, env, append(baseArgs(), st.args...)...)
	cmd.Stdout, cmd.Stderr = out, out
	err := runGroup(cmd)
	if err == nil {
		return 0, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code := exit.ExitCode()
		if containsInt(st.okCodes, code) {
			return code, nil
		}
		return code, errors.New("acme.sh exited with code " + itoa(code) + " (" + st.args[0] + ")")
	}
	return -1, err
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func itoa(i int) string {
	var b bytes.Buffer
	if i < 0 {
		b.WriteByte('-')
		i = -i
	}
	var digits []byte
	for i >= 10 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	digits = append([]byte{byte('0' + i)}, digits...)
	b.Write(digits)
	return b.String()
}

// ensurePrivateDirs keeps data/ and the TLS directory 0700, also when they
// exist from an earlier version with a wider mode.
func ensurePrivateDirs() error {
	for _, dir := range []string{dataDir(), certDir()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// runSync runs one short acme.sh command in the foreground (remove).
func runSync(env []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := ensurePrivateDirs(); err != nil {
		return "", err
	}
	cmd := command(ctx, env, append(baseArgs(), args...)...)
	out := &tailBuffer{}
	cmd.Stdout, cmd.Stderr = out, out
	err := runGroup(cmd)
	return out.String(), err
}

// runGroup runs cmd and then ends what is left of its process group, on
// every outcome: acme.sh can exit (or be killed) while a hook or curl it
// started still runs, and that must not outlive the job and its lock.
func runGroup(cmd *exec.Cmd) error {
	err := cmd.Run()
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	return err
}

// stepsTimeout is jobTimeout plus the DNS waits of the steps (up to an hour
// each), which acme.sh spends sleeping inside a step: an explicit
// --dnssleep, or the saved wait a renewal reloads.
func stepsTimeout(steps []step) time.Duration {
	timeout := jobTimeout
	for _, st := range steps {
		wait := st.dnsSleep
		for i, arg := range st.args {
			if arg == "--dnssleep" && i+1 < len(st.args) {
				if seconds, err := strconv.Atoi(st.args[i+1]); err == nil && seconds > wait {
					wait = seconds
				}
			}
		}
		if wait > 0 {
			timeout += time.Duration(min(wait, maxDNSSleep)) * time.Second
		}
	}
	return timeout
}

// removeLog deletes the acme.sh log, which can hold a lot after many runs.
func removeLog() {
	_ = os.Remove(filepath.Join(dataDir(), logName))
}
