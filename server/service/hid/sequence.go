package hid

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/inputcontrol"
)

// Key sequences the server presses on the host with fixed timing:
// Ctrl+Alt+Del, one Magic SysRq key, and REISUB (the SysRq restart
// sequence with a pause after each step so the host gets its work done).
// They run under the same manual reservation as paste, so the keyboard
// stays held by this request from the first report to the last release;
// a browser tab sending the steps itself cannot keep that timing.

type SequenceReq struct {
	// ctrl-alt-del, sysrq (with Key) or reisub (with DelayMs)
	Sequence string `form:"sequence" json:"sequence" validate:"required,oneof=ctrl-alt-del sysrq reisub"`
	// sysrq: the command letter or digit
	Key string `form:"key" json:"key"`
	// reisub: the pause after each step except the last; 0 picks the default
	DelayMs int `form:"delayMs" json:"delayMs"`
}

const (
	// how long a combination is held before it is released
	sequenceHold = 100 * time.Millisecond
	// the pause between two SysRq keys of REISUB; sync and remount need it
	defaultReisubDelay = 5 * time.Second
	minReisubDelay     = time.Second
	maxReisubDelay     = 30 * time.Second
	// a detached run gets this much beyond its own duration before it is cut
	detachedGrace = 10 * time.Second
)

const (
	hidModifierCtrlLeft = 0x01
	hidModifierAltLeft  = 0x04
	hidKeyPrintScreen   = 0x46 // SysRq
	hidKeyDelete        = 0x4c
)

// sysrqKeys are the Magic SysRq commands a user may send, by the key that
// selects them. The meaning is the kernel's (Documentation/admin-guide/sysrq.rst).
var sysrqKeys = map[string]byte{
	"b": 0x05, "c": 0x06, "d": 0x07, "e": 0x08, "f": 0x09, "h": 0x0b, "i": 0x0c,
	"j": 0x0d, "k": 0x0e, "l": 0x0f, "m": 0x10, "n": 0x11, "o": 0x12, "p": 0x13,
	"q": 0x14, "r": 0x15, "s": 0x16, "t": 0x17, "u": 0x18, "v": 0x19, "w": 0x1a,
	"z": 0x1d,
	"0": 0x27, "1": 0x1e, "2": 0x1f, "3": 0x20, "4": 0x21, "5": 0x22,
	"6": 0x23, "7": 0x24, "8": 0x25, "9": 0x26,
}

var reisub = []string{"r", "e", "i", "s", "u", "b"}

// sequenceStep is one keyboard report, held for hold, followed by a pause.
type sequenceStep struct {
	report []byte
	hold   time.Duration
	pause  time.Duration
}

func keyboardReport(modifiers byte, keys ...byte) []byte {
	report := make([]byte, 8)
	report[0] = modifiers
	copy(report[2:], keys)
	return report
}

// sysrqSteps presses Alt, adds SysRq, adds the command key, then lets the
// command key and SysRq go before Alt: the order a hand would use, so the
// host sees the chord build up rather than three keys at once.
func sysrqSteps(key byte, pause time.Duration) []sequenceStep {
	return []sequenceStep{
		{report: keyboardReport(hidModifierAltLeft), hold: sequenceHold},
		{report: keyboardReport(hidModifierAltLeft, hidKeyPrintScreen), hold: sequenceHold},
		{report: keyboardReport(hidModifierAltLeft, hidKeyPrintScreen, key), hold: sequenceHold},
		{report: keyboardReport(hidModifierAltLeft, hidKeyPrintScreen), hold: sequenceHold},
		{report: keyboardReport(0), hold: 0, pause: pause},
	}
}

// buildSequence turns a request into steps, or says why it cannot.
func buildSequence(req SequenceReq) ([]sequenceStep, error) {
	switch req.Sequence {
	case "ctrl-alt-del":
		return []sequenceStep{
			{report: keyboardReport(hidModifierCtrlLeft|hidModifierAltLeft, hidKeyDelete), hold: sequenceHold},
			{report: keyboardReport(0)},
		}, nil
	case "sysrq":
		code, ok := sysrqKeys[req.Key]
		if !ok {
			return nil, fmt.Errorf("unknown SysRq key %q", req.Key)
		}
		return sysrqSteps(code, 0), nil
	case "reisub":
		delay := defaultReisubDelay
		if req.DelayMs != 0 {
			delay = time.Duration(req.DelayMs) * time.Millisecond
			if delay < minReisubDelay || delay > maxReisubDelay {
				return nil, fmt.Errorf("REISUB delay must be between %v and %v", minReisubDelay, maxReisubDelay)
			}
		}
		var steps []sequenceStep
		for i, key := range reisub {
			pause := delay
			if i == len(reisub)-1 {
				pause = 0
			}
			steps = append(steps, sysrqSteps(sysrqKeys[key], pause)...)
		}
		return steps, nil
	}
	return nil, fmt.Errorf("unknown sequence %q", req.Sequence)
}

func sequenceDuration(steps []sequenceStep) time.Duration {
	var total time.Duration
	for _, step := range steps {
		total += step.hold + step.pause
	}
	return total
}

// runSequence writes the steps in order and always ends with every key
// released, also when the context ends halfway: a host must never be left
// with Alt+SysRq held. Every sequence already ends in a release; the
// deferred one is for the paths that do not get there.
func runSequence(ctx context.Context, steps []sequenceStep, write func([]byte) error) (err error) {
	release := keyboardReport(0)
	defer func() {
		if releaseErr := write(release); err == nil {
			err = releaseErr
		}
	}()
	for _, step := range steps {
		if err = context.Cause(ctx); err != nil {
			return err
		}
		if err = write(step.report); err != nil {
			return err
		}
		if step.hold > 0 {
			if err = sleepPasteContext(ctx, step.hold); err != nil {
				return err
			}
		}
		if step.pause > 0 {
			if err = sleepPasteContext(ctx, step.pause); err != nil {
				return err
			}
		}
	}
	return nil
}

// One sequence at a time: the coordinator serialises single reports, not
// whole sequences, and two REISUBs interleaved would be nonsense on the
// host. The state is what the status route shows.
type sequenceRun struct {
	mu         sync.Mutex
	running    bool
	sequence   string
	startedAt  time.Time
	duration   time.Duration
	lastResult *proto.KeySequenceResult
}

var sequences sequenceRun

// begin claims the single slot; ok is false while another sequence runs.
func (r *sequenceRun) begin(sequence string, duration time.Duration) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return false
	}
	r.running, r.sequence, r.startedAt, r.duration = true, sequence, time.Now(), duration
	return true
}

func (r *sequenceRun) end(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := &proto.KeySequenceResult{Sequence: r.sequence, Ok: err == nil, FinishedAt: time.Now().UTC().Format(time.RFC3339)}
	if err != nil {
		result.Error = err.Error()
	}
	r.lastResult = result
	r.running = false
}

func (r *sequenceRun) status() proto.KeySequenceStatusRsp {
	r.mu.Lock()
	defer r.mu.Unlock()
	rsp := proto.KeySequenceStatusRsp{Running: r.running, LastResult: r.lastResult}
	if r.running {
		rsp.Sequence = r.sequence
		rsp.StartedAt = r.startedAt.UTC().Format(time.RFC3339)
		rsp.DurationMs = int(r.duration / time.Millisecond)
		rsp.RemainingMs = int(max(0, r.duration-time.Since(r.startedAt)) / time.Millisecond)
	}
	return rsp
}

// GetSequenceStatus says whether a sequence is running and how the last
// one ended.
func (s *Service) GetSequenceStatus(c *gin.Context) {
	var rsp proto.Response
	rsp.OkRspWithData(c, sequences.status())
}

// SendSequence presses one of the fixed key sequences on the host.
// Ctrl+Alt+Del and a single SysRq key run within the request. REISUB runs
// detached: once R has gone out, stopping halfway (a closed tab, a dropped
// Wi-Fi link, the client's timeout) would leave the host with every process
// killed and nothing synced or rebooted, so the request returns at once with
// the duration and the steps go on by themselves; the status route follows.
func (s *Service) SendSequence(c *gin.Context) {
	var req SequenceReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}
	steps, err := buildSequence(req)
	if err != nil {
		rsp.ErrRsp(c, -2, err.Error())
		return
	}
	duration := sequenceDuration(steps)
	if !sequences.begin(req.Sequence, duration) {
		rsp.ErrRsp(c, -3, "HID control is busy")
		return
	}

	manual := s.newManualSession()
	reservation, err := manual.Reserve(c.Request.Context(), inputcontrol.ManualKeyboard, true, s.allowManual)
	if err != nil {
		manual.Close()
		sequences.end(err)
		log.Errorf("key sequence %s failed to acquire HID control: %v", req.Sequence, err)
		rsp.ErrRsp(c, -3, "HID control is busy")
		return
	}
	// Ctrl+Alt+Del and SysRq stop at the next press once another session
	// takes input control; their release still goes out. REISUB does not:
	// stopped after E or I the host is left with nothing synced or
	// rebooted, the reason it runs detached.
	owns := inputOwner(c)
	if req.Sequence == "reisub" {
		owns = func() bool { return true }
	}
	write := func(report []byte) error {
		return reservation.Execute(func() error {
			if !isKeyboardRelease(report) && !owns() {
				return ErrInputOwnerChanged
			}
			return s.hid.WriteKeyboardReport(report)
		})
	}
	run := func(ctx context.Context) error {
		defer manual.Close()
		err := runSequence(ctx, steps, write)
		reservation.Complete(err == nil)
		sequences.end(err)
		if errors.Is(err, ErrInputOwnerChanged) {
			log.Infof("key sequence %s stopped: %v", req.Sequence, err)
		} else if err != nil {
			log.Errorf("key sequence %s failed: %v", req.Sequence, err)
		} else {
			log.Debugf("key sequence %s sent in %v", req.Sequence, duration)
		}
		return err
	}

	if req.Sequence == "reisub" {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), duration+detachedGrace)
			defer cancel()
			_ = run(ctx)
		}()
		rsp.OkRspWithData(c, proto.KeySequenceStartedRsp{Sequence: req.Sequence, DurationMs: int(duration / time.Millisecond)})
		return
	}
	if err := run(c.Request.Context()); err != nil {
		if errors.Is(err, ErrInputOwnerChanged) {
			rsp.ErrRsp(c, InputOwnerCode, "another session holds input control")
			return
		}
		rsp.ErrRsp(c, -3, "key sequence failed")
		return
	}
	rsp.OkRspWithData(c, proto.KeySequenceStartedRsp{Sequence: req.Sequence, DurationMs: int(duration / time.Millisecond)})
}
