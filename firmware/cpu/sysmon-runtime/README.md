# Opt-in Go sysmon policies

These are the selected opt-in Go runtime modifications for NanoKVM on
Linux/riscv64: a sysmon timer slack and a sysmon sleep floor for GOMAXPROCS=1.
The source version and original proc.go checksum are pinned in
go-source-pin.json. The shared Go installation is not modified. Runtime source
is copied; other GOROOT components are symlinked and the base installation must
remain available. prepare.py patches three sites in the pinned proc.go: a
startup hook at the start of sysmon, a thread hook after minit in mstart1, and
the argument of sysmon's per-iteration usleep. Other OS/architecture
combinations have no-op hooks.

prepare.py also copies the std-vendored `golang.org/x/crypto/chacha20` and
`internal/poly1305` packages (their symlinked parent directories become
directories of symlinks), checks the pinned checksums of the vendored files it
relies on, and adds the C906 XTheadVector ChaCha20 and the riscv64 Poly1305
described in [go-crypto-c906](../go-crypto-c906/README.md).
nanokvm-manifest.json records the resulting vendored source checksums.

## Timer slack policy

`NANOKVM_SYSMON_TIMER_SLACK_NS=250000` gives only Go's sysmon thread a 250 us
timer coalescing allowance. This is an allowed delay, not a periodic timer or
a guaranteed wakeup interval. The runtime's retake, preemption and backoff
algorithms are unchanged. Unset, empty or `0` preserves the original policy;
`50000` is an explicit control. Other values are ignored with a runtime message.

Linux children inherit timer slack. The mstart1 hook restores the original
allowance on new Go threads when their inherited value equals the selected
sysmon value. It runs after minit, before the thread's mstartfn. The sysmon
startup hook records the original value before publishing the opt-in value.
Both hooks must run without a P; they use atomic words and a direct prctl
syscall, without allocation. This deliberately targets NanoKVM's runtime:
an independently configured child allowance equal to the sysmon override is
also restored. Foreign library threads which do not enter Go mstart1 are not
covered; inspect actual application threads when qualifying a build.

## Sleep floor policy

`NANOKVM_SYSMON_MIN_DELAY_US=1000` keeps each sysmon sleep at 1000 us or more
while `gomaxprocs == 1`. Accepted values are `500`, `1000` and `2000`. Unset,
empty or `0` preserves the original policy; other values are ignored with the
runtime message `ignored invalid NanoKVM sysmon minimum delay`.

Upstream sysmon restarts at 20 us sleeps whenever the program becomes busy
after all Ps were idle, and only starts doubling after 50 iterations without
work. With a capture loop that idles in the netpoller between frames, it
therefore polls several times per frame. With one P this polling rarely helps:
retaking the only P from a short syscall just hands it to another M,
preemption needs a 10 ms time slice and netpoll is only forced after 10 ms.

Only the sleep argument changes: sleep = max(floor, delay). sysmon's own
`delay` and `idle` state, the doubling to 10 ms, deep sleep and its wakeups,
retake rules, forcegc, the GOMAXPROCS update and the netpoll check are
unchanged. Above the floor the upstream backoff continues as before; doubling
still starts after 50 idle iterations, which now span about 50 floors instead
of about 1 ms. `gomaxprocs` is read on every iteration, so a run-time
GOMAXPROCS change applies from the next sysmon iteration. The floor is parsed
once by the sysmon startup hook into a word that only sysmon reads; the sleep
hook is inlined into sysmon, runs without a P and does not allocate.

Trade-off: with the floor F, sysmon notices a syscall in one iteration and
retakes the P in the next, so a P blocked in a syscall is retaken after one to
two periods of about F plus the sysmon timer slack (1.25 to 2.5 ms for 1000 +
250 us) instead of 20 to 40 us plus slack. The 10 ms preemption and netpoll
checks get the same extra granularity, so a long-running goroutine may run up
to about F longer before it is preempted, and a forced netpoll may come up to
about F later. With one P, upstream retakes every syscall that spans two
sysmon ticks even with an empty run queue, which mostly causes handoffs; with
the floor, a goroutine that is runnable while the only P's goroutine is
blocked in a syscall now waits up to that longer retake time. The policy is
inactive whenever gomaxprocs is greater than 1.

To disable it, set `NANOKVM_SYSMON_MIN_DELAY_US=0` (or empty) in the
environment of the server; kvmapp/system/init.d/S95nanokvm keeps an explicit
value and only defaults it to 1000 on the enhanced flavour. Alpine's OpenRC
nanokvm-app service starts the server through the same script.

The policies may delay sysmon's scheduler duties. A CPU saving does not establish
input latency, GC/preemption behavior, WebRTC pacing or endurance. Do not set
the allowance globally or hardcode a discovered thread ID in a boot script.

## Reproduce

Run from the repository root, replacing paths with explicit local locations:

```sh
python3 firmware/cpu/sysmon-runtime/prepare.py --base /path/to/go1.27.1 --output /new/path/to/go-nanokvm
for smoke in thread-policy-smoke sysmon-floor-smoke; do
  GOROOT=/new/path/to/go-nanokvm GOTOOLCHAIN=local GOOS=linux GOARCH=riscv64 CGO_ENABLED=0 /path/to/go1.27.1/bin/go build -trimpath -o /tmp/$smoke firmware/cpu/sysmon-runtime/$smoke.go
done
```

The output directory must not exist. The builder verifies source/version and
records modified source hashes in nanokvm-manifest.json. Set the same GOROOT
and GOTOOLCHAIN when invoking scripts/build-server-existing-libs.py with an
explicit matching native library set and Buildroot compiler. Do not run other
toolchain installation/update commands against the symlinked output.

On the device, the standalone thread-policy smoke checks 12 locked workers, the
main thread and all visible process threads, first with the variable unset,
then 250000, then 0. It assumes an inherited 50000 ns baseline. It reads
/proc/<TID>, since this kernel does not expose timerslack_ns beneath
/proc/self/task/<TID>. This checks the target syscall and thread policy, not
video or all possible thread creation parents.

The sleep floor smoke (`NANOKVM_SYSMON_TIMER_SLACK_NS=250000
/tmp/sysmon-floor-smoke [work-us]`) runs a 60 fps loop that waits for each frame
in the netpoller and then works for 4 ms with blocking syscalls. It counts the
sysmon thread's voluntary context switches (one per usleep or futex sleep)
with the floor unset, empty, 1000 (first with GOMAXPROCS=2, then switched to 1
at run time) and 2000, in three interleaved rounds compared by median, and
checks that an invalid value is rejected. Other processes share the single
core and the scheduler already stretches 20 us sleeps under load, so the
thresholds are loose and the absolute rates vary with device load.

Hardware/application results and limitations are summarized in
[validation](../../../docs/VALIDATION.md); these smoke tests do not qualify
browser latency.
