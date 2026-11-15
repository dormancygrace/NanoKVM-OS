package vm

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"NanoKVM-Server/internal/crontab"
	"NanoKVM-Server/proto"
)

// Scheduled tasks (Services > Scheduled tasks): a script of the Scripts
// window or a shell command, run by crond. They live in root's crontab
// itself, so that it stays the one place cron jobs are, and backups and
// settings recovery keep them as they keep that file: a comment line
// "# nanokvm-task {json}" per task holds its settings, and an enabled task
// has its job line after it, ending with "# nanokvm-task <id>". Every other
// line belongs to someone else and is kept as it is.
//
// The job runs through run-task, which keeps the first 64 KiB of its output
// and its exit status in /run/nanokvm/tasks (RAM, cleared at boot).

const taskMarker = "# nanokvm-task"

var (
	taskRunner = "/usr/libexec/nanokvm/run-task"
	taskRunDir = "/run/nanokvm/tasks"
	taskIDs    = regexp.MustCompile(`^t[0-9a-f]{8}$`)
)

type ScheduledTask struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// Five cron fields, in the device's time zone.
	Schedule string `json:"schedule"`
	// "script": Target is a script of the Scripts window; "command": a shell
	// command line.
	Kind   string `json:"kind"`
	Target string `json:"target"`
}

type taskStatus struct {
	ScheduledTask
	Next []int64 `json:"next"`
	// running, exited N, or empty when it has not run since the boot.
	State   string `json:"state,omitempty"`
	Started int64  `json:"started,omitempty"`
	Ended   int64  `json:"ended,omitempty"`
}

func validateTask(t ScheduledTask) error {
	if !taskIDs.MatchString(t.ID) {
		return errors.New("invalid task")
	}
	name := strings.TrimSpace(t.Name)
	if name == "" || utf8.RuneCountInString(name) > 64 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return errors.New("use a name of 1-64 characters")
	}
	if _, err := parseCron(t.Schedule); err != nil {
		return err
	}
	switch t.Kind {
	case "script":
		if _, err := scriptPath(ScriptDirectory, t.Target); err != nil {
			return errors.New("choose a script of the Scripts window")
		}
	case "command":
		if strings.TrimSpace(t.Target) == "" || len(t.Target) > 1024 || strings.ContainsAny(t.Target, "\r\n\x00") {
			return errors.New("use a command of one line, up to 1024 bytes")
		}
	default:
		return errors.New("a task runs a script or a command")
	}
	return nil
}

// shellQuote quotes one word for /bin/sh.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// taskArgs is what run-task runs: the working directory, then the program.
// A Python script is checked for its interpreter when it runs, not here.
func taskArgs(t ScheduledTask) []string {
	if t.Kind == "command" {
		return []string{"/root", "/bin/sh", "-c", t.Target}
	}
	path := filepath.Join(ScriptDirectory, t.Target)
	if strings.EqualFold(filepath.Ext(path), ".py") {
		return []string{filepath.Dir(path), pythonInterpreter, path}
	}
	return []string{filepath.Dir(path), path}
}

func taskJobLine(t ScheduledTask) string {
	words := []string{taskRunner, t.ID}
	for _, arg := range taskArgs(t) {
		words = append(words, shellQuote(arg))
	}
	return strings.Join(strings.Fields(t.Schedule), " ") + " " + strings.Join(words, " ") + " " + taskMarker + " " + t.ID
}

// parseTasks splits a crontab into our tasks and the other lines.
func parseTasks(data []byte) (tasks []ScheduledTask, other []string) {
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if config, ok := strings.CutPrefix(line, taskMarker+" {"); ok {
			var t ScheduledTask
			if json.Unmarshal([]byte("{"+config), &t) == nil && taskIDs.MatchString(t.ID) {
				tasks = append(tasks, t)
				continue
			}
		}
		if i := strings.LastIndex(line, " "+taskMarker+" "); i >= 0 && taskIDs.MatchString(line[i+len(taskMarker)+2:]) {
			continue // a job line: written again from its task
		}
		other = append(other, line)
	}
	if len(other) == 1 && other[0] == "" {
		other = nil
	}
	return tasks, other
}

func renderTasks(tasks []ScheduledTask, other []string) []byte {
	lines := append([]string{}, other...)
	for _, t := range tasks {
		config, _ := json.Marshal(t)
		lines = append(lines, taskMarker+" "+string(config))
		if t.Enabled {
			lines = append(lines, taskJobLine(t))
		}
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

// editTasks changes the task list in the crontab under its lock; crond is
// enabled and started when a task is enabled.
func editTasks(change func([]ScheduledTask) ([]ScheduledTask, error)) error {
	crontab.Mu.Lock()
	defer crontab.Mu.Unlock()
	data, err := crontab.Read()
	if err != nil {
		return err
	}
	tasks, other := parseTasks(data)
	if tasks, err = change(tasks); err != nil {
		return err
	}
	for _, t := range tasks {
		if t.Enabled {
			if err = crontab.EnsureDaemon(); err != nil {
				return err
			}
			break
		}
	}
	return crontab.Install(renderTasks(tasks, other))
}

func readTaskState(id string) (state string, started, ended int64) {
	data, err := os.ReadFile(filepath.Join(taskRunDir, id+".status"))
	if err != nil {
		return "", 0, 0
	}
	// "running START" or "exited CODE START END"
	f := strings.Fields(string(data))
	if len(f) >= 2 && f[0] == "running" {
		started, _ = strconv.ParseInt(f[1], 10, 64)
		return "running", started, 0
	}
	if len(f) == 4 && f[0] == "exited" {
		started, _ = strconv.ParseInt(f[2], 10, 64)
		ended, _ = strconv.ParseInt(f[3], 10, 64)
		return "exited " + f[1], started, ended
	}
	return "", 0, 0
}

func unixTimes(times []time.Time) []int64 {
	out := make([]int64, 0, len(times))
	for _, t := range times {
		out = append(out, t.Unix())
	}
	return out
}

func (s *Service) GetScheduledTasks(c *gin.Context) {
	var rsp proto.Response
	crontab.Mu.Lock()
	data, err := crontab.Read()
	crontab.Mu.Unlock()
	if err != nil {
		rsp.ErrRsp(c, -1, "cannot read the crontab")
		return
	}
	tasks, other := parseTasks(data)
	now := time.Now().In(deviceLocation())
	list := make([]taskStatus, 0, len(tasks))
	for _, t := range tasks {
		st := taskStatus{ScheduledTask: t}
		if schedule, err := parseCron(t.Schedule); err == nil && t.Enabled {
			st.Next = unixTimes(schedule.next(now, 3))
		}
		st.State, st.Started, st.Ended = readTaskState(t.ID)
		list = append(list, st)
	}
	// Other jobs, shown read-only: comments and blank lines are left out.
	jobs := []string{}
	for _, line := range other {
		if l := strings.TrimSpace(line); l != "" && !strings.HasPrefix(l, "#") {
			jobs = append(jobs, l)
		}
	}
	_, daemonErr := crontab.Exec(nil, "rc-service", "crond", "status")
	rsp.OkRspWithData(c, gin.H{"tasks": list, "other": jobs, "crond": daemonErr == nil})
}

func (s *Service) SaveScheduledTask(c *gin.Context) {
	var rsp proto.Response
	var t ScheduledTask
	if err := c.ShouldBindJSON(&t); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}
	if t.ID == "" {
		var random [4]byte
		_, _ = rand.Read(random[:])
		t.ID = "t" + hex.EncodeToString(random[:])
	}
	t.Name = strings.TrimSpace(t.Name)
	t.Schedule = strings.Join(strings.Fields(t.Schedule), " ")
	if err := validateTask(t); err != nil {
		rsp.ErrRsp(c, -2, err.Error())
		return
	}
	err := editTasks(func(tasks []ScheduledTask) ([]ScheduledTask, error) {
		for i := range tasks {
			if tasks[i].ID == t.ID {
				tasks[i] = t
				return tasks, nil
			}
		}
		if len(tasks) >= 64 {
			return nil, errors.New("at most 64 scheduled tasks")
		}
		return append(tasks, t), nil
	})
	if err != nil {
		rsp.ErrRsp(c, -1, err.Error())
		return
	}
	rsp.OkRspWithData(c, gin.H{"id": t.ID})
}

func (s *Service) DeleteScheduledTask(c *gin.Context) {
	var rsp proto.Response
	var req struct {
		ID string `json:"id"`
	}
	if c.ShouldBindJSON(&req) != nil || !taskIDs.MatchString(req.ID) {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}
	err := editTasks(func(tasks []ScheduledTask) ([]ScheduledTask, error) {
		kept := tasks[:0]
		for _, t := range tasks {
			if t.ID != req.ID {
				kept = append(kept, t)
			}
		}
		return kept, nil
	})
	if err != nil {
		rsp.ErrRsp(c, -1, err.Error())
		return
	}
	_ = os.Remove(filepath.Join(taskRunDir, req.ID+".status"))
	_ = os.Remove(filepath.Join(taskRunDir, req.ID+".log"))
	rsp.OkRsp(c)
}

// PreviewSchedule checks a schedule and returns its next runs.
func (s *Service) PreviewSchedule(c *gin.Context) {
	var rsp proto.Response
	var req struct {
		Schedule string `json:"schedule"`
	}
	if c.ShouldBindJSON(&req) != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}
	schedule, err := parseCron(req.Schedule)
	if err != nil {
		rsp.ErrRsp(c, -2, err.Error())
		return
	}
	rsp.OkRspWithData(c, gin.H{"next": unixTimes(schedule.next(time.Now().In(deviceLocation()), 5))})
}

// RunScheduledTask runs a task now, as cron would, in a session of its own.
func (s *Service) RunScheduledTask(c *gin.Context) {
	var rsp proto.Response
	var req struct {
		ID string `json:"id"`
	}
	if c.ShouldBindJSON(&req) != nil || !taskIDs.MatchString(req.ID) {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}
	crontab.Mu.Lock()
	data, err := crontab.Read()
	crontab.Mu.Unlock()
	if err != nil {
		rsp.ErrRsp(c, -1, "cannot read the crontab")
		return
	}
	tasks, _ := parseTasks(data)
	for _, t := range tasks {
		if t.ID != req.ID {
			continue
		}
		cmd := exec.Command(taskRunner, append([]string{t.ID}, taskArgs(t)...)...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			rsp.ErrRsp(c, -1, "the task could not be started")
			return
		}
		go func() { _ = cmd.Wait() }()
		rsp.OkRsp(c)
		return
	}
	rsp.ErrRsp(c, -1, "task not found")
}

func (s *Service) GetScheduledTaskLog(c *gin.Context) {
	var rsp proto.Response
	id := c.Query("id")
	if !taskIDs.MatchString(id) {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}
	data, err := os.ReadFile(filepath.Join(taskRunDir, id+".log"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		rsp.ErrRsp(c, -1, "cannot read the task output")
		return
	}
	rsp.OkRspWithData(c, gin.H{"log": string(data)})
}
