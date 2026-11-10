# Function order of the server

The C906 has a 2-way 32 KiB instruction cache, no second level, and a small
TLB. `NanoKVM-Server` has 10 MiB of Go code, and the Go linker lays functions
out package by package: the few hundred functions a WebRTC video stream runs
are spread over all of it. Streaming 2560x1440 H.265 at 60 fps (21 Mbit/s,
2100 packets a second), the server took about 180 instruction-cache misses per
packet in user mode, at about 180 cycles each, and its code pages kept falling
out of the TLB.

The server is therefore linked with the functions of that workload first,
those that call each other next to each other:

- `prepare-linker.py` gives the Go linker of the prepared toolchain the option
  `-hotorder=FILE`: the functions named in FILE are laid out first, in that
  order, the rest after them as before. It is 30 lines in
  `cmd/link/internal/ld` beside the linker's own `-randlayout`, applied to a
  copy; the linker is rebuilt into the prepared GOROOT, the base toolchain is
  not written to. Functions of one package are then no longer neighbours, so
  the linker decides about trampolines as it does for a random layout.
- `server-webrtc.txt` is the order: 312 functions, 138 KiB, 97% of the
  server's samples in the profile below. A name the program does not have is
  ignored, so a stale list costs speed, not correctness.
- `platform/build.sh` runs `prepare-linker.py` after the runtime is prepared
  and `scripts/build-server-existing-libs.py` links with
  `-hotorder=server-webrtc.txt`.

## Measured

NanoKVM PCIe, the stream above over Wi-Fi, the same kernel and engine driver,
A/B/A; busy share of the core over 15 s windows and a 120 s sampling profile:

| | default order | this order |
| --- | ---: | ---: |
| busy | 35.3 to 36.6% | 32.1 to 32.9% |
| server, user mode: samples of 120 000 | 28 426 | 24 753 |
| server, user mode: instruction-cache misses | 46.8 M | 34.3 M |
| server, user mode: TLB misses | 26.1 M | 17.4 M |
| server, in the kernel: instruction-cache misses | 25.3 M | 20.2 M |
| instructions per 15 s | 1167 to 1228 M | 1163 to 1253 M |

The same instructions run; fewer of them wait. The kernel gains too, because
the server evicts less of its code.

Two things that did not help, measured the same way: the 300 hottest functions
in order of their samples, without regard to who calls whom, gave about half
of this (busy 33.7 to 35.5%); and Go code without compressed instructions,
where every branch target is aligned to 4 bytes but the code is 25% larger,
was slower (10% more instruction-cache misses).

## Making the order again

The order belongs to a build of the server and a workload. After changes to
the video path, or when the numbers above no longer hold:

1. Build `profile/pmuprof.c` as a module for the device's kernel. It samples
   the interrupted pc about 1000 times a second (a kernel without perf
   events), with the return address register for user samples and the
   instruction, cache-miss and TLB-miss counts of each process.
2. On the device, with the stream running: `insmod pmuprof.ko`, wait two
   minutes, save `/proc/pmuprof`, `rmmod pmuprof`. Take the server binary that
   ran (`/kvmapp/server/NanoKVM-Server`).
3. `go run profile/hotorder.go -binary NanoKVM-Server -profile pmuprof.txt >
   server-webrtc.txt`. A sample's pc names the function, its return address
   the caller while the function has called nothing yet; that is enough for
   call edges without unwinding. Functions covering 97% of the samples are
   clustered as hfsort's C3 does (a callee's cluster follows its caller's
   while the two stay within 16 KiB) and the clusters laid out by samples per
   byte.
4. Build, and compare the busy share and the misses with the previous order
   on the same stream.

The profile this order was made from: 26 729 user samples of the server in
120 s, on the build of 2026-11-10 with the engine's cipher-and-hash chain in
use.
