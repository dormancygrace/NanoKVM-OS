package hid

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func TestBuildSequenceCtrlAltDel(t *testing.T) {
	steps, err := buildSequence(SequenceReq{Sequence: "ctrl-alt-del"})
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 || !bytes.Equal(steps[0].report, []byte{0x05, 0, 0x4c, 0, 0, 0, 0, 0}) || !bytes.Equal(steps[1].report, make([]byte, 8)) {
		t.Fatalf("steps %+v", steps)
	}
}

func TestBuildSequenceSysRq(t *testing.T) {
	steps, err := buildSequence(SequenceReq{Sequence: "sysrq", Key: "h"})
	if err != nil {
		t.Fatal(err)
	}
	// Alt, Alt+SysRq, Alt+SysRq+H, Alt+SysRq, released
	want := [][]byte{
		{0x04, 0, 0, 0, 0, 0, 0, 0},
		{0x04, 0, 0x46, 0, 0, 0, 0, 0},
		{0x04, 0, 0x46, 0x0b, 0, 0, 0, 0},
		{0x04, 0, 0x46, 0, 0, 0, 0, 0},
		{0, 0, 0, 0, 0, 0, 0, 0},
	}
	if len(steps) != len(want) {
		t.Fatalf("%d steps", len(steps))
	}
	for i, step := range steps {
		if !bytes.Equal(step.report, want[i]) {
			t.Fatalf("step %d: %v", i, step.report)
		}
	}
	for _, key := range []string{"", "a", "x", "H", "rr"} {
		if _, err := buildSequence(SequenceReq{Sequence: "sysrq", Key: key}); err == nil {
			t.Fatalf("SysRq %q accepted", key)
		}
	}
}

func TestBuildSequenceReisub(t *testing.T) {
	steps, err := buildSequence(SequenceReq{Sequence: "reisub"})
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 6*5 {
		t.Fatalf("%d steps", len(steps))
	}
	// the command keys in order, each as the third report of its chord
	var letters []byte
	var pauses []time.Duration
	for i := 0; i < len(steps); i += 5 {
		letters = append(letters, steps[i+2].report[3])
		pauses = append(pauses, steps[i+4].pause)
	}
	if !bytes.Equal(letters, []byte{0x15, 0x08, 0x0c, 0x16, 0x18, 0x05}) {
		t.Fatalf("letters %v", letters)
	}
	for i, pause := range pauses {
		want := defaultReisubDelay
		if i == 5 {
			want = 0
		}
		if pause != want {
			t.Fatalf("pause %d: %v", i, pause)
		}
	}
	// four held reports per chord (the release is not held), five pauses
	if sequenceDuration(steps) != 5*defaultReisubDelay+6*4*sequenceHold {
		t.Fatalf("duration %v", sequenceDuration(steps))
	}
	if _, err := buildSequence(SequenceReq{Sequence: "reisub", DelayMs: 500}); err == nil {
		t.Fatal("a 500 ms delay was accepted")
	}
	if _, err := buildSequence(SequenceReq{Sequence: "reisub", DelayMs: 40000}); err == nil {
		t.Fatal("a 40 s delay was accepted")
	}
	custom, err := buildSequence(SequenceReq{Sequence: "reisub", DelayMs: 2000})
	if err != nil || custom[4].pause != 2*time.Second {
		t.Fatalf("custom delay: %v %v", err, custom[4].pause)
	}
	if _, err := buildSequence(SequenceReq{Sequence: "bogus"}); err == nil {
		t.Fatal("an unknown sequence was accepted")
	}
}

func TestRunSequenceWritesInOrderAndReleases(t *testing.T) {
	var written [][]byte
	write := func(report []byte) error {
		written = append(written, append([]byte(nil), report...))
		return nil
	}
	steps := []sequenceStep{
		{report: keyboardReport(0x05, 0x4c), hold: time.Millisecond},
		{report: keyboardReport(0)},
	}
	if err := runSequence(context.Background(), steps, write); err != nil {
		t.Fatal(err)
	}
	// the two steps, then the release the runner always adds
	if len(written) != 3 || !bytes.Equal(written[0], steps[0].report) || !bytes.Equal(written[2], make([]byte, 8)) {
		t.Fatalf("written %v", written)
	}
}

func TestRunSequenceReleasesWhenCancelledMidway(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	var written [][]byte
	write := func(report []byte) error {
		written = append(written, append([]byte(nil), report...))
		if len(written) == 2 {
			cancel(errors.New("browser gone"))
		}
		return nil
	}
	steps, _ := buildSequence(SequenceReq{Sequence: "reisub", DelayMs: 1000})
	err := runSequence(ctx, steps, write)
	if err == nil || err.Error() != "browser gone" {
		t.Fatalf("err %v", err)
	}
	last := written[len(written)-1]
	if !bytes.Equal(last, make([]byte, 8)) {
		t.Fatalf("the keyboard was left at %v", last)
	}
	if len(written) > 4 {
		t.Fatalf("%d reports after the cancel", len(written))
	}
}

func TestSequenceRunIsSingleFlightAndReportsStatus(t *testing.T) {
	var r sequenceRun
	if s := r.status(); s.Running || s.LastResult != nil {
		t.Fatalf("fresh status %+v", s)
	}
	if !r.begin("reisub", 27*time.Second) {
		t.Fatal("the first sequence was refused")
	}
	if r.begin("sysrq", time.Second) {
		t.Fatal("a second sequence ran alongside the first")
	}
	s := r.status()
	if !s.Running || s.Sequence != "reisub" || s.DurationMs != 27000 || s.RemainingMs > 27000 || s.RemainingMs <= 0 {
		t.Fatalf("running status %+v", s)
	}
	r.end(errors.New("EIO"))
	s = r.status()
	if s.Running || s.LastResult == nil || s.LastResult.Ok || s.LastResult.Error != "EIO" || s.LastResult.Sequence != "reisub" {
		t.Fatalf("finished status %+v", s)
	}
	if !r.begin("ctrl-alt-del", 200*time.Millisecond) {
		t.Fatal("the slot was not freed")
	}
	r.end(nil)
	if s := r.status(); s.Running || !s.LastResult.Ok {
		t.Fatalf("status after success %+v", s)
	}
}

func TestRunSequenceReleasesAfterAWriteError(t *testing.T) {
	var written [][]byte
	write := func(report []byte) error {
		written = append(written, append([]byte(nil), report...))
		if len(written) == 1 {
			return errors.New("EIO")
		}
		return nil
	}
	steps, _ := buildSequence(SequenceReq{Sequence: "ctrl-alt-del"})
	if err := runSequence(context.Background(), steps, write); err == nil {
		t.Fatal("the write error was lost")
	}
	if !bytes.Equal(written[len(written)-1], make([]byte, 8)) {
		t.Fatal("no release after the error")
	}
}
