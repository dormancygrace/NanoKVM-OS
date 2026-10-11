package acme

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
	jobTimeout  = 20 * time.Minute
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

// command runs acme.sh with an explicit environment: only PATH, HOME and the
// acme.sh directories plus the provider variables, under umask 077 so the
// files it creates are private.
func command(ctx context.Context, env []string, args ...string) *exec.Cmd {
	full := append([]string{"-c", `umask 077; exec "$0" "$@"`, script()}, args...)
	cmd := exec.CommandContext(ctx, "sh", full...)
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
}

// startJob runs the steps in the background, one job at a time, then calls
// after with the result. It returns at once; the page polls the status.
func startJob(action string, env, secrets []string, steps []step, after func(j *Job)) error {
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
		jobMu.Lock()
		lastJob = result
		jobActive = false
		jobMu.Unlock()
		if after != nil {
			after(result)
		}
	}()
	return nil
}

func runSteps(job *Job, env, secrets []string, steps []step) *Job {
	ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
	defer cancel()
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
			result.Error = "acme.sh did not finish within " + jobTimeout.String()
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
	cmd := command(ctx, env, append(baseArgs(), st.args...)...)
	cmd.Stdout, cmd.Stderr = out, out
	err := cmd.Run()
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

// runSync runs one short acme.sh command in the foreground (remove).
func runSync(env []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := command(ctx, env, append(baseArgs(), args...)...)
	out := &tailBuffer{}
	cmd.Stdout, cmd.Stderr = out, out
	err := cmd.Run()
	return out.String(), err
}

// removeLog deletes the acme.sh log, which can hold a lot after many runs.
func removeLog() {
	_ = os.Remove(filepath.Join(dataDir(), logName))
}
