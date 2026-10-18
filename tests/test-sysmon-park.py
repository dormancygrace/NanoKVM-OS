#!/usr/bin/env python3
# Exercise the prepared runtime's actual park policy and outer sysmon condition
# with deterministic scheduler state; no timing assertions or live-runtime writes.
import argparse
import os
from pathlib import Path
import re
import subprocess
import tempfile

parser = argparse.ArgumentParser()
parser.add_argument("--goroot", type=Path, required=True)
parser.add_argument("--go", default="go")
args = parser.parse_args()
source = (args.goroot / "src/runtime/nanokvm_sysmon_linux_riscv64.go").read_text()
policy = source[source.index("func nanokvmSysmonDelay("):source.index("func nanokvmThreadInit()")]
policy = re.sub(r"^//go:.*$", "", policy, flags=re.M)
proc = (args.goroot / "src/runtime/proc.go").read_text()
conditions = re.findall(r"^\s*if (debug.schedtrace <= 0.*nanokvmSysmonMayPark.*) \{$", proc, re.M)
assert len(conditions) == 1, "missing or ambiguous prepared sysmon park hook"
condition = conditions[0].replace("debug.schedtrace", "schedtrace").replace("sched.gcwaiting.Load()", "gcwaiting").replace("sched.npidle.Load() == gomaxprocs", "idle")
pre = r'''
package main
import "fmt"
var gomaxprocs int32 = 1
var nanokvmSysmonMinDelay uint32
var allpLock int
var locks int
func lock(*int) { locks++ }
func unlock(*int) { locks-- }
type p struct { schedtick, syscalltick uint32 }
var allp = []*p{{}}
var value string
func gogetenv(string) string { return value }
func reset(grace string) {
    value = grace
    nanokvmSysmonParkGrace = 0
    nanokvmSysmonParkGraceInit()
    nanokvmSysmonQuiet = false
    nanokvmSysmonTick = 0
    nanokvmSysmonQuietSince = 0
    nanokvmSysmonMinDelay = 0
    gomaxprocs = 1
    allp[0] = &p{schedtick: 1}
}
var checks int
func check(ok bool, message string) {
    checks++
    if !ok { panic(message) }
    if locks != 0 { panic("unbalanced allpLock") }
}
'''
post = r'''
func main() {
    const start int64 = 1e9
    for _, grace := range []string{"50", "100", "200"} {
        reset(grace)
        span := nanokvmSysmonParkGrace
        check(span > 0, "valid grace not enabled")
        check(!mayPark(start, false, true, 0), "idle activity must start grace")
        check(nanokvmSysmonDelay(20) == 10000, "quiet grace must use 10 ms")
        check(mayPark(start+1, true, true, 0), "GC must bypass grace")
        check(nanokvmSysmonDelay(20) == 20, "GC park leaked quiet delay after syscall wake")
        reset(grace)
        check(!mayPark(start, false, true, 0), "initial grace")
        check(!mayPark(start+span-1, false, true, 0), "parked before grace")
        allp[0].syscalltick++
        check(!mayPark(start+span, false, true, 0), "syscall must restart grace")
        check(!mayPark(start+2*span-1, false, true, 0), "restarted grace too short")
        check(mayPark(start+2*span, false, true, 0), "quiet P must eventually park")
        check(nanokvmSysmonDelay(20) == 20, "normal park leaked quiet delay")
        allp[0].schedtick++
        check(!mayPark(start+3*span, false, true, 0), "schedule must restart grace")
        check(!mayPark(start+3*span+1, false, false, 0), "busy P must not park")
        check(nanokvmSysmonDelay(20) == 20, "busy P retained quiet delay")
        check(!mayPark(start+4*span, false, true, 0), "busy time must restart grace")
        gomaxprocs = 2
        check(mayPark(start+4*span+1, false, true, 0), "multi-P must use upstream park")
        check(nanokvmSysmonDelay(20) == 20, "multi-P quiet delay")
        gomaxprocs = 1
        allp[0].schedtick++
        check(!mayPark(start+5*span, false, true, 0), "return to one P")
        check(mayPark(start+5*span+1, true, false, 0), "GC with busy P must park")
        nanokvmSysmonMinDelay = 1000
        check(nanokvmSysmonDelay(20) == 1000, "GC wake must retain explicit sleep floor")
        check(!mayPark(start+6*span, true, true, 1), "schedtrace must still prevent park")
    }
    for _, grace := range []string{"", "0", "75"} {
        reset(grace)
        check(nanokvmSysmonParkGrace == 0, "disabled/invalid grace enabled")
        check(mayPark(start, false, true, 0), "disabled idle behavior")
        check(!mayPark(start, false, false, 0), "disabled busy behavior")
        check(mayPark(start, true, false, 0), "disabled GC behavior")
        check(nanokvmSysmonDelay(20) == 20, "disabled delay")
    }
    fmt.Printf("PASS: %d park-grace transition checks\n", checks)
}
'''
with tempfile.TemporaryDirectory(prefix="sysmon-park-") as temporary:
    path = Path(temporary) / "policy.go"
    path.write_text(pre + policy + "\nfunc mayPark(now int64, gcwaiting, idle bool, schedtrace int) bool { return " + condition + " }\n" + post)
    env = os.environ.copy()
    env.update(GOOS="linux", GOARCH="amd64", CGO_ENABLED="0", GOTOOLCHAIN="local", GOEXPERIMENT="")
    env.pop("GOROOT", None)
    result = subprocess.run([args.go, "run", str(path)], env=env, text=True, capture_output=True, timeout=120)
    print(result.stdout, end="")
    if result.returncode:
        raise SystemExit(result.stderr)
    assert result.stderr == "runtime: ignored invalid NanoKVM sysmon park grace\n", result.stderr
