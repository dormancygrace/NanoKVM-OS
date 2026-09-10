# Opt-in Go sysmon timer slack

This is the selected opt-in Go runtime modification for NanoKVM on Linux/riscv64. The
source version and original proc.go checksum are pinned in go-source-pin.json.
The shared Go installation is not modified. Runtime source is copied; other
GOROOT components are symlinked and the base installation must remain available.

## Policy

`NANOKVM_SYSMON_TIMER_SLACK_NS=250000` gives only Go's sysmon thread a 250 us
timer coalescing allowance. This is an allowed delay, not a periodic timer or
a guaranteed wakeup interval. The runtime's retake, preemption and backoff
algorithms are unchanged. Unset, empty or `0` preserves the original policy;
`50000` is an explicit control. Other values are ignored with a runtime message.
Other OS/architecture combinations have no-op hooks.

Linux children inherit timer slack. The mstart1 hook restores the original
allowance on new Go threads when their inherited value equals the selected
sysmon value. It runs after minit, before the thread's mstartfn. The sysmon
startup hook records the original value before publishing the opt-in value.
Both hooks must run without a P; they use atomic words and a direct prctl
syscall, without allocation. This deliberately targets NanoKVM's runtime:
an independently configured child allowance equal to the sysmon override is
also restored. Foreign library threads which do not enter Go mstart1 are not
covered; inspect actual application threads when qualifying a build.

The policy may delay sysmon's scheduler duties. A CPU saving does not establish
input latency, GC/preemption behavior, WebRTC pacing or endurance. Do not set
the allowance globally or hardcode a discovered thread ID in a boot script.

## Reproduce

Run from the repository root, replacing paths with explicit local locations:

```sh
python3 firmware/cpu/sysmon-runtime/prepare.py --base /path/to/go1.27.1 --output /new/path/to/go-nanokvm
GOROOT=/new/path/to/go-nanokvm GOTOOLCHAIN=local GOOS=linux GOARCH=riscv64 CGO_ENABLED=0 /path/to/go1.27.1/bin/go build -trimpath -o /tmp/thread-policy-smoke firmware/cpu/sysmon-runtime/thread-policy-smoke.go
```

The output directory must not exist. The builder verifies source/version and
records modified source hashes in nanokvm-manifest.json. Set the same GOROOT
and GOTOOLCHAIN when invoking scripts/build-server-existing-libs.py with an
explicit matching native library set and Buildroot compiler. Do not run other
toolchain installation/update commands against the symlinked output.

On the device, the standalone smoke checks 12 locked workers, the main thread
and all visible process threads, first with the variable unset, then 250000,
then 0. It assumes an inherited 50000 ns baseline. It reads /proc/<TID>, since
this kernel does not expose timerslack_ns beneath /proc/self/task/<TID>.
This checks the target syscall and thread policy, not video or all possible
thread creation parents. Hardware/application results and limitations are
summarized in [validation](../../../docs/VALIDATION.md); this thread-policy smoke test does not qualify browser latency.
