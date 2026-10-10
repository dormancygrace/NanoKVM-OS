package direct

import (
	"strings"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

func captureDebugLog(t *testing.T) *logtest.Hook {
	t.Helper()
	hook := logtest.NewGlobal()
	level := log.GetLevel()
	log.SetLevel(log.DebugLevel)
	t.Cleanup(func() {
		log.SetLevel(level)
		hook.Reset()
	})
	return hook
}

func countLines(hook *logtest.Hook, prefix string) int {
	count := 0
	for _, entry := range hook.AllEntries() {
		if strings.HasPrefix(entry.Message, prefix) {
			count++
		}
	}
	return count
}

func TestHealthyConnectionHasNoDropSummary(t *testing.T) {
	var probe keyframeProbe
	q := probe.attach(newFrameQueue(4, 1024))
	q.offer(keyOfSize(1, 4))
	q.offer(deltaOfSize(2, 4))
	if summary := q.summary(); summary != "" {
		t.Fatalf("summary = %q, want none", summary)
	}
}

func TestDropCountersSeparateTheCauses(t *testing.T) {
	var probe keyframeProbe
	q := probe.attach(newFrameQueue(2, 100))

	q.offer(keyOfSize(1, 10))     // 19 bytes; ends the join wait
	q.offer(deltaOfSize(2, 10))   // 19 bytes
	q.offer(deltaOfSize(3, 10))   // frame count overflow: 2 queued + incoming
	q.offer(deltaOfSize(4, 10))   // refused while waiting
	q.offer(keyOfSize(5, 10))     // recovery 1
	q.offer(deltaOfSize(6, 85))   // byte overflow: 19 queued + 94 incoming
	q.offer(keyOfSize(7, 10))     // recovery 2
	q.markDiscontinuity()         // queued keyframe lost
	q.markDiscontinuity()         // same sequence
	q.offer(keyOfSize(8, 10))     // recovery 3
	q.requestResync()             // the browser decoder gave up
	q.offer(keyOfSize(9, 100))    // does not fit: still waiting
	q.offer(keyOfSize(10, 10))    // recovery 4
	q.offer(deltaOfSize(11, 100)) // alone above the byte limit

	d := q.diag
	if d.overflows != 3 || d.overflowFrames != 3+2+2 {
		t.Fatalf("overflows = %d frames = %d", d.overflows, d.overflowFrames)
	}
	if d.overflowBytes != 57+113+128 {
		t.Fatalf("overflow bytes = %d", d.overflowBytes)
	}
	if d.discontinuities != 1 || d.discontinuityFrames != 1 {
		t.Fatalf("discontinuities = %d frames = %d", d.discontinuities, d.discontinuityFrames)
	}
	if d.oversizedKeyframes != 1 || d.oversizedDeltas != 1 || d.resyncs != 1 {
		t.Fatalf("oversized keyframes/deltas = %d/%d resyncs = %d",
			d.oversizedKeyframes, d.oversizedDeltas, d.resyncs)
	}
	if d.deltasRefused != 1 {
		t.Fatalf("refused deltas = %d, want 1", d.deltasRefused)
	}
	if d.recoveries != 5 {
		t.Fatalf("recoveries = %d, want 5 (join and four drop sequences)", d.recoveries)
	}
	for _, field := range []string{"overflows=3", "discontinuities=1", "oversized_keyframes=1", "resyncs=1"} {
		if !strings.Contains(q.summary(), field) {
			t.Fatalf("summary %q lacks %s", q.summary(), field)
		}
	}
}

func TestDropDebugLinesAreRateLimitedAndRecoveryIsReported(t *testing.T) {
	hook := captureDebugLog(t)
	var probe keyframeProbe
	q := probe.attach(newFrameQueue(1, 1024))

	for i := range 5 {
		q.offer(keyOfSize(int64(10*i), 4))
		q.offer(deltaOfSize(int64(10*i+1), 4)) // overflow, one sequence per loop
		probe.advance(100 * time.Millisecond)
	}
	if got := countLines(hook, "direct stream drop:"); got != 1 {
		t.Fatalf("%d drop lines within the interval, want 1", got)
	}
	if got := countLines(hook, "direct stream recovered:"); got != 5 {
		t.Fatalf("%d recovery lines, want 5", got)
	}

	probe.advance(dropLogInterval)
	q.offer(keyOfSize(100, 4))
	q.offer(deltaOfSize(101, 4))
	if got := countLines(hook, "direct stream drop:"); got != 2 {
		t.Fatalf("%d drop lines after the interval, want 2", got)
	}
	for _, entry := range hook.AllEntries() {
		if strings.HasPrefix(entry.Message, "direct stream drop:") && !strings.Contains(entry.Message, "reason=queue-frames") {
			t.Fatalf("drop line without a reason: %q", entry.Message)
		}
	}
}

func TestDropLinesAreSilentAboveDebugLevel(t *testing.T) {
	hook := logtest.NewGlobal()
	level := log.GetLevel()
	log.SetLevel(log.InfoLevel)
	t.Cleanup(func() { log.SetLevel(level); hook.Reset() })

	var probe keyframeProbe
	q := probe.attach(newFrameQueue(1, 1024))
	q.offer(keyOfSize(1, 4))
	q.offer(deltaOfSize(2, 4))
	q.offer(keyOfSize(3, 4))
	if len(hook.AllEntries()) != 0 {
		t.Fatalf("unexpected log output: %v", hook.AllEntries())
	}
	if q.summary() == "" {
		t.Fatal("counters must run regardless of the log level")
	}
}
