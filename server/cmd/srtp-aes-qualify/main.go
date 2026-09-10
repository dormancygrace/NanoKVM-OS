package main

import (
	"NanoKVM-Server/internal/sg2002aes"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/srtp/v3"
)

type counted struct {
	device      *sg2002aes.Device
	calls, hits int
}

func (c *counted) TryXORKeyStream(k, iv, dst, src []byte) bool {
	c.calls++
	ok := c.device.TryXORKeyStream(k, iv, dst, src)
	if ok {
		c.hits++
	}
	return ok
}
func check(err error) {
	if err != nil {
		panic(err)
	}
}
func context(backend srtp.AESCTRAccelerator) *srtp.Context {
	srtp.SetAESCTRAccelerator(backend)
	c, e := srtp.CreateContext([]byte("0123456789abcdef"), []byte("0123456789abcd"), srtp.ProtectionProfileAes128CmHmacSha1_80)
	check(e)
	return c
}
func packet(size int, seq uint16) []byte {
	p := make([]byte, size+12)
	p[0] = 0x80
	p[1] = 0x80 | 102
	binary.BigEndian.PutUint16(p[2:4], seq)
	binary.BigEndian.PutUint32(p[4:8], uint32(seq)*1500)
	binary.BigEndian.PutUint32(p[8:12], 0x12345678)
	for j := 12; j < len(p); j++ {
		p[j] = byte(j*31 + int(seq))
	}
	return p
}
func equal(want, got []byte) {
	if !bytes.Equal(want, got) {
		panic("ciphertext/plaintext mismatch")
	}
}
func cpu() int64 {
	var u syscall.Rusage
	check(syscall.Getrusage(syscall.RUSAGE_SELF, &u))
	return u.Utime.Sec*1000000 + u.Utime.Usec + u.Stime.Sec*1000000 + u.Stime.Usec
}

// benchmarkPayloads validates the total hardware exposure before opening a device.
func benchmarkPayloads(list string, packets, rounds int) ([]int, error) {
	if packets < 1 || packets > 2000 || rounds < 1 || rounds > 3 {
		return nil, fmt.Errorf("packets must be 1..2000 and rounds 1..3")
	}
	fields := strings.Split(list, ",")
	if len(fields) > 8 {
		return nil, fmt.Errorf("at most 8 payload sizes")
	}
	sizes := make([]int, 0, len(fields))
	seen := make(map[int]bool)
	for _, field := range fields {
		size, err := strconv.Atoi(strings.TrimSpace(field))
		if err != nil || size < sg2002aes.MinPayload || size > sg2002aes.MaxPayload || seen[size] {
			return nil, fmt.Errorf("payload sizes must be unique integers in %d..%d", sg2002aes.MinPayload, sg2002aes.MaxPayload)
		}
		sizes = append(sizes, size)
		seen[size] = true
	}
	// Reserve more than the existing small correctness set needs.
	if len(sizes)*rounds*(packets+100)+32 > 10000 {
		return nil, fmt.Errorf("hardware operation budget exceeds 10000 including warmups and correctness")
	}
	return sizes, nil
}

func main() {
	payloadList := flag.String("payloads", "1188", "comma-separated hardware-eligible RTP payload sizes")
	packets := flag.Int("packets", 2000, "timed packets per mode, size and round (1..2000)")
	rounds := flag.Int("rounds", 3, "alternating rounds (1..3)")
	flag.Parse()
	payloads, err := benchmarkPayloads(*payloadList, *packets, *rounds)
	check(err)
	if flag.NArg() != 0 {
		panic("unexpected positional arguments")
	}
	deadline := time.Now().Add(20 * time.Second)
	checkBudget := func() {
		if time.Now().After(deadline) {
			panic("20-second wall budget exhausted between operations; cannot cancel a stuck ioctl")
		}
	}

	d, e := sg2002aes.Open(func(e error) { panic(e) })
	check(e)
	defer d.Close()
	hw := &counted{device: d}
	soft, acc, dec := context(nil), context(hw), context(nil)
	sizes := []int{0, 1, 15, 16, 511, 512, 513, 1188, 1200, 4095, 4096, 4097}
	for i, size := range sizes {
		raw := packet(size, uint16(65530+i))
		want, e := soft.EncryptRTP(nil, raw, nil)
		check(e)
		// Exercise exact in-place encryption with sufficient capacity for the tag.
		in := make([]byte, len(raw), len(raw)+32)
		copy(in, raw)
		got, e := acc.EncryptRTP(in, in, nil)
		check(e)
		equal(want, got)
		plain, e := dec.DecryptRTP(nil, got, nil)
		check(e)
		equal(raw, plain)
		got[len(got)-1] ^= 1
		if _, e = dec.DecryptRTP(nil, got, nil); e == nil {
			panic("tampered tag accepted")
		}
	}
	rr := &rtcp.ReceiverReport{SSRC: 0x12345678, Reports: make([]rtcp.ReceptionReport, 31)}
	raw, e := rr.Marshal()
	check(e)
	for i := 0; i < 4; i++ {
		want, e := soft.EncryptRTCP(nil, raw, nil)
		check(e)
		got, e := acc.EncryptRTCP(nil, raw, nil)
		check(e)
		equal(want, got)
		plain, e := dec.DecryptRTCP(nil, got, nil)
		check(e)
		equal(raw, plain)
	}
	fmt.Printf("qualification=PASS rtp_sizes=%d sequence_rollover=true inplace=true tamper=true srtcp=4 hardware_calls=%d hardware_hits=%d\n", len(sizes), hw.calls, hw.hits)
	enc := json.NewEncoder(os.Stdout)
	for round := 0; round < *rounds; round++ {
		modes := []string{"software", "hardware"}
		if round%2 == 1 {
			modes = []string{"hardware", "software"}
		}
		for _, size := range payloads {
			for _, mode := range modes {
				checkBudget()
				c := context(nil)
				if mode == "hardware" {
					c = context(hw)
				}
				p := packet(size, 0)
				dst := make([]byte, 0, len(p)+32)
				for i := 0; i < 100; i++ {
					binary.BigEndian.PutUint16(p[2:4], uint16(i))
					dst, e = c.EncryptRTP(dst[:0], p, nil)
					check(e)
				}
				// Verify the final warmup at every selected size outside the timed loop.
				want, e := context(nil).EncryptRTP(nil, p, nil)
				check(e)
				equal(want, dst)
				start := time.Now()
				before := cpu()
				hits := hw.hits
				for i := 100; i < 100+*packets; i++ {
					if (i-100)%128 == 0 {
						checkBudget()
					}
					binary.BigEndian.PutUint16(p[2:4], uint16(i))
					dst, e = c.EncryptRTP(dst[:0], p, nil)
					check(e)
				}
				cost := cpu() - before
				wall := time.Since(start)
				if mode == "hardware" && hw.hits-hits != *packets {
					panic("benchmark silently fell back to software")
				}
				check(enc.Encode(map[string]any{"mode": mode, "round": round, "packets": *packets, "payload": size, "cpu_us_packet": float64(cost) / float64(*packets), "wall_us_packet": float64(wall.Nanoseconds()) / 1000 / float64(*packets), "hardware_hits": hw.hits - hits}))
			}
		}
	}

	// Closed device must decline without damaging the in-place input.
	check(d.Close())
	soft, acc = context(nil), context(hw)
	raw = packet(1188, 1)
	want, e := soft.EncryptRTP(nil, raw, nil)
	check(e)
	got, e := acc.EncryptRTP(raw, raw, nil)
	check(e)
	equal(want, got)
	fmt.Println("closed_device_software_fallback=PASS")
}
