package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	sha "loadprobe/sha"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

type meter struct {
	PID       atomic.Int64
	Memory    map[string]int64
	W         io.Writer
	Histogram map[int]int
	Alignment map[uintptr]int
	Bytes     int64
}

func (m *meter) Write(p []byte) (int, error) {
	n, e := m.W.Write(p)
	if m.Alignment == nil {
		m.Alignment = map[uintptr]int{}
	}
	if len(p) > 0 {
		m.Alignment[uintptr(unsafe.Pointer(&p[0]))%64]++
	}
	m.Histogram[n]++
	m.Bytes += int64(n)
	if m.Memory == nil && m.Bytes >= 8388608 && m.PID.Load() > 0 {
		m.Memory = map[string]int64{}
		data, _ := os.ReadFile(fmt.Sprintf("/proc/%d/smaps_rollup", m.PID.Load()))
		for _, l := range strings.Split(string(data), "\n") {
			var name, unit string
			var k int64
			if _, e := fmt.Sscanf(l, "%s %d %s", &name, &k, &unit); e == nil && unit == "kB" {
				m.Memory[strings.TrimSuffix(name, ":")] = k
			}
		}
	}
	return n, e
}
func frame() int64 {
	p, _ := os.ReadFile("/proc/cvitek/venc")
	f := strings.Fields(string(p))
	for i, x := range f {
		if x == "EncodedFrame:" && i+1 < len(f) {
			n, _ := strconv.ParseInt(f[i+1], 10, 64)
			return n
		}
	}
	return -1
}
func run() error {
	rate := flag.Int("rate", 0, "curl byte rate limit")
	profile := flag.String("profile", "baseline", "OpenSSL runtime")
	bufferSize := flag.Int("buffer", 0, "write buffer")
	flag.Parse()
	if *bufferSize != 0 && *bufferSize != 65536 && *bufferSize != 262144 {
		return fmt.Errorf("invalid buffer")
	}
	var fs syscall.Statfs_t
	if e := syscall.Statfs("/data", &fs); e != nil {
		return e
	}
	if uint64(fs.Bavail)*uint64(fs.Bsize) < 64*1024*1024 {
		return fmt.Errorf("insufficient data space")
	}
	f, e := os.CreateTemp("/data", ".nkos-load-audit-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	headers := f.Name() + ".headers"
	defer os.Remove(headers)
	h := sha.NewSHA256()
	sink := &meter{W: f, Histogram: map[int]int{}}
	var output io.Writer = sink
	var bw *bufio.Writer
	if *bufferSize > 0 {
		bw = bufio.NewWriterSize(sink, *bufferSize)
		output = bw
	}
	m := &meter{W: output, Histogram: map[int]int{}}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	args := []string{"--disable", "--silent", "--show-error", "--fail", "--verbose", "--location", "--max-redirs", "10", "--connect-timeout", "10", "--max-time", "30", "--proto", "=https", "--proto-redir", "=https", "--tlsv1.2", "--tls13-ciphers", "TLS_CHACHA20_POLY1305_SHA256", "--ciphers", "ECDHE-RSA-CHACHA20-POLY1305:ECDHE-ECDSA-CHACHA20-POLY1305", "--interface", "wlan0", "--resolve", "releases.ubuntu.com:443:185.125.190.37", "--range", "0-16777215", "--max-filesize", "16777216", "--dump-header", headers, "--url", "https://releases.ubuntu.com/26.04.1/ubuntu-26.04.1-live-server-amd64.iso"}
	if *rate > 0 {
		args = append(args, "--limit-rate", strconv.Itoa(*rate))
	}
	cmd := exec.CommandContext(ctx, "/usr/bin/curl", args...)
	cmd.Env = append(os.Environ(), "OPENSSL_CONF=/dev/null")
	if *profile == "c906-o3" || *profile == "c906-auto" {
		cmd.Env = append(cmd.Env, "LD_LIBRARY_PATH=/tmp/nkos-load-audit/"+*profile+"/usr/lib", "OPENSSL_MODULES=/tmp/nkos-load-audit/"+*profile+"/usr/lib/ossl-modules")
	} else if *profile != "baseline" {
		return fmt.Errorf("invalid profile")
	}
	cmd.Stdout = io.MultiWriter(m, h)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	var self0, self1, child0, child1 syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &self0)
	syscall.Getrusage(syscall.RUSAGE_CHILDREN, &child0)
	v0 := frame()
	begin := time.Now()
	e = cmd.Start()
	if e == nil {
		m.PID.Store(int64(cmd.Process.Pid))
		syscall.Setpriority(syscall.PRIO_PROCESS, cmd.Process.Pid, 10)
		e = cmd.Wait()
	}
	if e == nil && bw != nil {
		e = bw.Flush()
	}
	elapsed := time.Since(begin).Seconds()
	syscall.Getrusage(syscall.RUSAGE_SELF, &self1)
	syscall.Getrusage(syscall.RUSAGE_CHILDREN, &child1)
	hdr, _ := os.ReadFile(headers)
	var selected []string
	for _, s := range strings.Split(stderr.String(), "\n") {
		if strings.Contains(s, "SSL connection using") || strings.Contains(s, "< HTTP/") || strings.Contains(s, "curl:") {
			selected = append(selected, s)
		}
	}
	if e != nil {
		return fmt.Errorf("curl: %v; %s", e, strings.Join(selected, "; "))
	}
	if m.Bytes != 16777216 || !bytes.Contains(hdr, []byte("206 Partial Content")) {
		return fmt.Errorf("unexpected range: bytes=%d partial=%t", m.Bytes, bytes.Contains(hdr, []byte("206 Partial Content")))
	}
	tv := func(a syscall.Timeval) float64 { return float64(a.Sec) + float64(a.Usec)/1e6 }
	v1 := frame()
	result := map[string]any{"profile": *profile, "rate_limit_bytes": *rate, "curl_peak_rss_kib": child1.Maxrss, "curl_streaming_memory_kib": m.Memory, "peer": "185.125.190.37", "encoded_fps": float64(v1-v0) / elapsed, "input_alignment_mod64": m.Alignment, "bytes": m.Bytes, "seconds": elapsed, "destination": "private SD data file, synced outside measurement then removed", "buffer_bytes": *bufferSize, "file_write_histogram": sink.Histogram, "purpose": "same installed curl with baseline or C906-O3 OpenSSL; production SHA256 path and SD; handshake included, fsync/final SHA tail excluded", "write_size_histogram": m.Histogram, "sha256": hex.EncodeToString(h.Sum(nil)), "tls": selected, "helper_user_seconds": tv(self1.Utime) - tv(self0.Utime), "helper_kernel_seconds": tv(self1.Stime) - tv(self0.Stime), "curl_user_seconds": tv(child1.Utime) - tv(child0.Utime), "curl_kernel_seconds": tv(child1.Stime) - tv(child0.Stime)}
	if e = f.Sync(); e != nil {
		return e
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
