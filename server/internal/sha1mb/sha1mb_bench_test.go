package sha1mb

import (
	"crypto/hmac"
	"crypto/sha1"
	"flag"
	"hash"
	"math/rand"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// frame returns SRTP-like messages header || payload || ROC for packets of
// the given sizes (header included).
func frame(sizes ...int) []Message {
	rnd := rand.New(rand.NewSource(3))
	roc := []byte{0, 0, 0, 1}
	msgs := make([]Message, len(sizes))
	for i, n := range sizes {
		p := make([]byte, n)
		rnd.Read(p)
		msgs[i] = Message{p[:12], p[12:], roc}
	}
	return msgs
}

var workloads = []struct {
	name string
	msgs []Message
}{
	{"video 13x1200+600", frame(1200, 1200, 1200, 1200, 1200, 1200, 1200, 1200, 1200, 1200, 1200, 1200, 1200, 600)},
	{"audio 4x172", frame(172, 172, 172, 172)},
	{"pair 2x1200", frame(1200, 1200)},
	{"single 1x1200", frame(1200)},
	{"bulk 4x4096", frame(4096, 4096, 4096, 4096)},
}

type variant struct {
	name string
	run  func(tags [][Size]byte, msgs []Message)
}

func variants(tb testing.TB) []variant {
	key := make([]byte, 20)
	k := NewKey(key)
	mac := hmac.New(sha1.New, key)
	l := []variant{
		// pion/srtp: one hmac.Hash per context, Reset, Write packet, Write ROC, Sum.
		{"crypto/hmac", func(tags [][Size]byte, msgs []Message) { hmacEach(mac, tags, msgs) }},
		{"generic", k.sumGeneric},
	}
	if haveVector() {
		l = append(l, variant{"vector", func(tags [][Size]byte, msgs []Message) { k.sumLanes(tags, msgs, kernel) }})
	}
	// The Go part of the vector path alone: scheduling, segments, copies.
	nop := func(*Message, *byte, *seg, *[4][2]uint32, *[2][5]uint32, *[Size]byte, int) {}
	l = append(l, variant{"setup only", func(tags [][Size]byte, msgs []Message) { k.sumLanes(tags, msgs, nop) }})
	return l
}

func hmacEach(mac hash.Hash, tags [][Size]byte, msgs []Message) {
	for i, m := range msgs {
		mac.Reset()
		mac.Write(m[0])
		mac.Write(m[1])
		mac.Write(m[2])
		mac.Sum(tags[i][:0])
	}
}

func BenchmarkSumBatch(b *testing.B) {
	for _, w := range workloads {
		for _, v := range variants(b) {
			b.Run(w.name+"/"+v.name, func(b *testing.B) {
				tags := make([][Size]byte, len(w.msgs))
				bytes := 0
				for _, m := range w.msgs {
					bytes += len(m[0]) + len(m[1]) + len(m[2])
				}
				b.SetBytes(int64(bytes))
				b.ReportAllocs()
				for b.Loop() {
					v.run(tags, w.msgs)
				}
			})
		}
	}
}

var (
	measureRounds   = flag.Int("sha1mb.measure", 0, "rounds of TestMeasure (0 skips it)")
	measureWorkload = flag.String("sha1mb.workload", "", "measure only workloads whose name contains this")
)

// threadCPU returns the CPU time of the calling thread.
func threadCPU() time.Duration {
	const clockThreadCPUTimeID = 3
	var ts syscall.Timespec
	syscall.Syscall(syscall.SYS_CLOCK_GETTIME, clockThreadCPUTimeID, uintptr(unsafe.Pointer(&ts)), 0)
	return time.Duration(ts.Nano())
}

// TestMeasure reports the thread CPU time per packet of each variant, the
// median of interleaved rounds of about 150 ms each. Other processes share
// the single core, so CPU time is the stable figure.
func TestMeasure(t *testing.T) {
	if *measureRounds == 0 {
		t.Skip("enable with -sha1mb.measure=ROUNDS")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	for _, w := range workloads {
		if !strings.Contains(w.name, *measureWorkload) {
			continue
		}
		vs := variants(t)
		tags := make([][Size]byte, len(w.msgs))
		iters := make([]int, len(vs))
		for i, v := range vs {
			n := 1
			for {
				c0 := threadCPU()
				for range n {
					v.run(tags, w.msgs)
				}
				if threadCPU()-c0 > 20*time.Millisecond {
					break
				}
				n *= 2
			}
			iters[i] = n * 8
		}
		res := make([][]float64, len(vs))
		for range *measureRounds {
			for i, v := range vs {
				runtime.GC()
				c0 := threadCPU()
				for range iters[i] {
					v.run(tags, w.msgs)
				}
				c := threadCPU() - c0
				res[i] = append(res[i], c.Seconds()*1e6/float64(iters[i]*len(w.msgs)))
			}
		}
		base := 0.0
		for i, v := range vs {
			slices.Sort(res[i])
			med := res[i][len(res[i])/2]
			if i == 0 {
				base = med
			}
			t.Logf("MEASURE %-18s %-12s %7.2f us/packet (min %.2f, max %.2f) x%.2f",
				w.name, v.name, med, res[i][0], res[i][len(res[i])-1], base/med)
		}
	}
}
