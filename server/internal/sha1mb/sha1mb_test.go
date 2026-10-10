package sha1mb

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"os"
	"regexp"
	"runtime"
	"runtime/pprof"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var (
	crossRuns  = flag.Int("sha1mb.runs", 2000, "random batches per implementation in TestCrossCheck")
	stressDur  = flag.Duration("sha1mb.stress", 3*time.Second, "duration of TestStress")
	implFilter = flag.String("sha1mb.impl", "", "regexp selecting the implementations to check (default all)")
)

type impl struct {
	name string
	sum  func(k *Key, tags [][Size]byte, msgs []Message)
}

// impls returns the implementations to compare with crypto/hmac: the
// generic code, the Go model of the kernel (any platform) and the vector
// kernel (when the CPU has it), each called directly whatever the batch size.
func impls() []impl {
	l := []impl{
		{"generic", func(k *Key, t [][Size]byte, m []Message) { k.sumGeneric(t, m) }},
		{"model", func(k *Key, t [][Size]byte, m []Message) { k.sumLanes(t, m, kernelModel) }},
	}
	if haveVector() {
		l = append(l, impl{"vector", func(k *Key, t [][Size]byte, m []Message) { k.sumLanes(t, m, kernel) }})
	}
	l = append(l, impl{"SumBatch", (*Key).SumBatch})
	if *implFilter != "" {
		re := regexp.MustCompile(*implFilter)
		l = slices.DeleteFunc(l, func(im impl) bool { return !re.MatchString(im.name) })
	}
	return l
}

func reference(key []byte, m Message) [Size]byte {
	mac := hmac.New(sha1.New, key)
	mac.Write(m[0])
	mac.Write(m[1])
	mac.Write(m[2])
	var t [Size]byte
	mac.Sum(t[:0])
	return t
}

func TestRFC2202(t *testing.T) {
	// RFC 2202 section 3, test cases 1, 2, 3, 6 and 7.
	cases := []struct{ key, data, tag string }{
		{"0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b", hex.EncodeToString([]byte("Hi There")),
			"b617318655057264e28bc0b6fb378c8ef146be00"},
		{hex.EncodeToString([]byte("Jefe")), hex.EncodeToString([]byte("what do ya want for nothing?")),
			"effcdf6ae5eb2fa2d27416d5f184df9c259a7c79"},
		{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", hex.EncodeToString(bytes.Repeat([]byte{0xdd}, 50)),
			"125d7342b9ac11cd91a39af48aa17b4f63f175d3"},
		{hex.EncodeToString(bytes.Repeat([]byte{0xaa}, 80)),
			hex.EncodeToString([]byte("Test Using Larger Than Block-Size Key - Hash Key First")),
			"aa4ae5e15272d00e95705637ce8a3b55ed402112"},
		{hex.EncodeToString(bytes.Repeat([]byte{0xaa}, 80)),
			hex.EncodeToString([]byte("Test Using Larger Than Block-Size Key and Larger Than One Block-Size Data")),
			"e8e99d0f45237d786d6bbaa7965c7808bbff1a91"},
	}
	for _, im := range impls() {
		for i, c := range cases {
			key, _ := hex.DecodeString(c.key)
			data, _ := hex.DecodeString(c.data)
			msgs := []Message{{data}, {data[:3], data[3:]}, {nil, nil, data}, {data}}
			tags := make([][Size]byte, len(msgs))
			im.sum(NewKey(key), tags, msgs)
			for j := range tags {
				if got := hex.EncodeToString(tags[j][:]); got != c.tag {
					t.Errorf("%s: case %d message %d: got %s, want %s", im.name, i, j, got, c.tag)
				}
			}
		}
	}
}

// randomMessage returns a message of length n split into up to three parts
// at random points (parts may be empty or nil), each part at a random offset
// in its own buffer.
func randomMessage(rnd *rand.Rand, n int) Message {
	data := make([]byte, n)
	rnd.Read(data)
	var cut [2]int
	switch rnd.Intn(4) {
	case 0: // one part
		cut = [2]int{n, n}
	case 1: // SRTP: 12 byte header, payload, 4 byte ROC
		if n >= 16 {
			cut = [2]int{12, n - 4}
			break
		}
		fallthrough
	default:
		cut = [2]int{rnd.Intn(n + 1), rnd.Intn(n + 1)}
		if cut[0] > cut[1] {
			cut[0], cut[1] = cut[1], cut[0]
		}
	}
	var m Message
	prev := 0
	for p := range 3 {
		end := n
		if p < 2 {
			end = cut[p]
		}
		if end == prev && rnd.Intn(2) == 0 {
			prev = end
			continue // nil part
		}
		off := rnd.Intn(8)
		buf := make([]byte, off+end-prev+rnd.Intn(8))
		rnd.Read(buf)
		m[p] = buf[off : off+end-prev]
		copy(m[p], data[prev:end])
		prev = end
	}
	return m
}

func randomLength(rnd *rand.Rand) int {
	switch r := rnd.Intn(10); {
	case r < 6:
		return rnd.Intn(1501)
	case r < 8: // around block and padding boundaries
		return 64*rnd.Intn(24) + 52 + rnd.Intn(16)
	default:
		return rnd.Intn(4097)
	}
}

func checkBatch(t *testing.T, im impl, k *Key, key []byte, msgs []Message) bool {
	t.Helper()
	saved := make([]Message, len(msgs))
	for i, m := range msgs {
		for p := range m {
			if m[p] != nil {
				saved[i][p] = bytes.Clone(m[p])
			}
		}
	}
	tags := make([][Size]byte, len(msgs)+1)
	tags[len(msgs)] = [Size]byte{1, 2, 3}
	im.sum(k, tags[:len(msgs)], msgs)
	ok := true
	for i, m := range msgs {
		if want := reference(key, m); tags[i] != want {
			t.Errorf("%s: batch of %d, message %d (parts %d/%d/%d): tag %x, want %x", im.name, len(msgs), i,
				len(m[0]), len(m[1]), len(m[2]), tags[i], want)
			ok = false
		}
		for p := range m {
			if !bytes.Equal(m[p], saved[i][p]) {
				t.Errorf("%s: message %d part %d modified", im.name, i, p)
				ok = false
			}
		}
	}
	if tags[len(msgs)] != [Size]byte{1, 2, 3} {
		t.Errorf("%s: wrote past tags", im.name)
		ok = false
	}
	return ok
}

// TestCrossCheck compares random batches with crypto/hmac: batch sizes
// 1..32, lengths 0..1500 and up to 4096, random part splits, equal and
// unequal lengths, random keys of 0..100 bytes.
func TestCrossCheck(t *testing.T) {
	for _, im := range impls() {
		rnd := rand.New(rand.NewSource(1))
		batches, messages := 0, 0
		for run := 0; run < *crossRuns; run++ {
			key := make([]byte, rnd.Intn(101))
			if run%2 == 0 {
				key = make([]byte, 20) // the SRTP auth key size
			}
			rnd.Read(key)
			k := NewKey(key)
			msgs := make([]Message, 1+rnd.Intn(32))
			equal := randomLength(rnd)
			for i := range msgs {
				n := equal
				if run%3 != 0 {
					n = randomLength(rnd)
				}
				msgs[i] = randomMessage(rnd, n)
			}
			if !checkBatch(t, im, k, key, msgs) {
				return
			}
			batches++
			messages += len(msgs)
		}
		t.Logf("%s: %d batches, %d messages match crypto/hmac", im.name, batches, messages)
	}
}

// TestBoundaries runs every message length 0..300 and every split of a few
// lengths around block boundaries, in batches of 1 to 7.
func TestBoundaries(t *testing.T) {
	key := []byte("0123456789abcdefghij")
	k := NewKey(key)
	for _, im := range impls() {
		count := 0
		var msgs []Message
		flush := func() bool {
			ok := checkBatch(t, im, k, key, msgs)
			count += len(msgs)
			msgs = msgs[:0]
			return ok
		}
		for n := 0; n <= 300; n++ {
			data := bytes.Repeat([]byte{byte(n)}, n)
			msgs = append(msgs, Message{data})
			if len(msgs) == 1+n%7 && !flush() {
				return
			}
		}
		for _, n := range []int{55, 56, 63, 64, 65, 119, 120, 128, 192} {
			data := make([]byte, n)
			for i := range data {
				data[i] = byte(i * 7)
			}
			for a := 0; a <= n; a++ {
				for b := a; b <= n; b += 1 + n/16 {
					msgs = append(msgs, Message{data[:a], data[a:b], data[b:]})
					if len(msgs) == 5 && !flush() {
						return
					}
				}
			}
		}
		if len(msgs) > 0 && !flush() {
			return
		}
		t.Logf("%s: %d messages match crypto/hmac", im.name, count)
	}
}

func TestEmpty(t *testing.T) {
	NewKey(nil).SumBatch(nil, nil)
	defer func() {
		if recover() == nil {
			t.Error("no panic for len(tags) != len(msgs)")
		}
	}()
	NewKey(nil).SumBatch(make([][Size]byte, 1), make([]Message, 2))
}

// TestLarge checks batches above maxBatch, which SumBatch splits.
func TestLarge(t *testing.T) {
	rnd := rand.New(rand.NewSource(2))
	key := make([]byte, 20)
	rnd.Read(key)
	msgs := make([]Message, maxBatch+maxBatch/2+3)
	for i := range msgs {
		msgs[i] = randomMessage(rnd, rnd.Intn(200))
	}
	checkBatch(t, impl{"SumBatch", (*Key).SumBatch}, NewKey(key), key, msgs)
}

func TestSelection(t *testing.T) {
	env := os.Getenv("NANOKVM_SHA1MB")
	t.Logf("haveVector %v, NANOKVM_SHA1MB=%q, vector kernel selected: %v", haveVector(), env, vector())
	if want := haveVector() && env != "generic"; vector() != want {
		t.Errorf("vector() = %v, want %v", vector(), want)
	}
}

func TestISAParse(t *testing.T) {
	for _, c := range []struct {
		cpuinfo string
		want    bool
	}{
		{"processor\t: 0\nhart\t\t: 0\nisa\t\t: rv64imafdc_zicntr_zicsr_zifencei_zihpm_xtheadvector\nmmu\t\t: sv39\n", true},
		{"isa\t\t: rv64imafdc_zicntr_zicsr_zifencei_zihpm\n", false},
		{"isa : rv64imafdcv_xtheadvectorx\n", false},
		{"isa : rv64gc_xtheadvector\nisa : rv64gc\n", false},
		{"isa : rv64gc_xtheadvector\r\nisa : rv64gc_xtheadvector\n", true},
		{"processor : 0\n", false},
		{"", false},
	} {
		if got := isaHasExtension([]byte(c.cpuinfo), "xtheadvector"); got != c.want {
			t.Errorf("%q: got %v", c.cpuinfo, got)
		}
	}
}

var stressSink atomic.Pointer[[]byte]

// TestStress runs SumBatch in several goroutines with a CPU profile
// (SIGPROF), garbage collection and preemption, and checks every tag. Run
// two copies of the test binary at once to also exercise context switches
// between processes using vector state.
func TestStress(t *testing.T) {
	if err := pprof.StartCPUProfile(io.Discard); err == nil {
		defer pprof.StopCPUProfile()
	}
	deadline := time.Now().Add(*stressDur)
	var wg sync.WaitGroup
	var batches, failures atomic.Int64
	for g := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rnd := rand.New(rand.NewSource(int64(100 + g)))
			key := make([]byte, 20)
			rnd.Read(key)
			k := NewKey(key)
			msgs := make([]Message, 4+8*g)
			want := make([][Size]byte, len(msgs))
			for i := range msgs {
				msgs[i] = randomMessage(rnd, 100+rnd.Intn(1400))
				want[i] = reference(key, msgs[i])
			}
			tags := make([][Size]byte, len(msgs))
			for time.Now().Before(deadline) {
				clear(tags)
				k.SumBatch(tags, msgs)
				for i := range tags {
					if tags[i] != want[i] {
						failures.Add(1)
						t.Errorf("goroutine %d: message %d: tag %x, want %x", g, i, tags[i], want[i])
						return
					}
				}
				garbage := make([]byte, 4096) // keep the GC busy
				stressSink.Store(&garbage)
				if batches.Add(1)%8 == 0 {
					runtime.Gosched()
				}
			}
		}()
	}
	wg.Wait()
	t.Logf("%d verified batches in %v (vector kernel: %v)", batches.Load(), *stressDur, vector())
}

func ExampleKey_SumBatch() {
	// One SRTP frame: header || payload || ROC for every packet, under the
	// session's 20-byte auth key.
	key := make([]byte, 20)
	k := NewKey(key)
	roc := []byte{0, 0, 0, 0}
	var packets [][]byte
	for range 3 {
		packets = append(packets, make([]byte, 1200)) // 12 byte header + payload
	}
	msgs := make([]Message, len(packets))
	for i, p := range packets {
		msgs[i] = Message{p[:12], p[12:], roc}
	}
	tags := make([][Size]byte, len(msgs))
	k.SumBatch(tags, msgs)
	fmt.Printf("%x\n", tags[0][:10]) // the 80-bit SRTP tag
	// Output: 0446731c09f5933da464
}
