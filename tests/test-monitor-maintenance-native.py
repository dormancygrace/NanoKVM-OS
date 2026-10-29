#!/usr/bin/env python3
"""Exercise real Go EDID bindings: maintenance, state preservation and failures.

Only filesystem paths are redirected with a Go overlay. A recording native ABI
makes any full capture stop/start in either EDID method a test failure.
"""
from pathlib import Path
import json
import os
import subprocess
import tempfile

root = Path(__file__).resolve().parents[1]
with tempfile.TemporaryDirectory(prefix="nanokvm-monitor-native-") as temporary:
    out = Path(temporary)
    journal = out / "calls"
    board = out / "hw"
    pending = out / "pending"
    utility = out / "update-edid"
    utility.write_text("#!/usr/bin/env python3\n" + r'''
import os, pathlib, sys
with open(os.environ["CALLS"], "a") as log:
    log.write("utility\n")
fail = os.environ["FAIL"]
if "command" in fail:
    sys.exit(1)
if sys.argv[1] == "--dump":
    pathlib.Path(sys.argv[2]).write_bytes(bytes(255 if fail == "size" else 256))
elif os.environ["BOARD"] == "beta":
    assert sys.argv[1:] == ["--accept-power-cycle", "profile"]
    assert pathlib.Path(os.environ["PENDING"]).read_text() == "1\n"
else:
    assert sys.argv[1:] == ["profile"]
''')
    utility.chmod(0o755)
    replacements = {}
    for relative, substitutions in {
        "server/common/kvm_vision.go": {
            '"/usr/sbin/nanokvm_update_edid"': json.dumps(str(utility)),
            'os.CreateTemp("/run",': f'os.CreateTemp({json.dumps(str(out))},',
        },
        "server/common/video_status.go": {
            '"/etc/kvm/hw"': json.dumps(str(board)),
            '"/etc/kvm/monitor_power_cycle_pending"': json.dumps(str(pending)),
        },
    }.items():
        source = root / relative
        text = source.read_text()
        for old, new in substitutions.items():
            assert old in text, old
            text = text.replace(old, new)
        replacement = out / source.name
        replacement.write_text(text)
        replacements[str(source)] = str(replacement)
    overlay = out / "overlay.json"
    overlay.write_text(json.dumps({"Replace": replacements}))
    (out / "main.go").write_text(r'''package main
import("fmt";"os";"strings";"NanoKVM-Server/common")
func require(ok bool, message string) {if !ok {panic(message)}}
func main() {
    k := new(common.KvmVision)
    enabled := os.Getenv("ENABLED") == "1"
    require(k.SetHDMI(enabled) == 0, "initial control")
    failure := os.Getenv("FAIL")
    var data []byte
    var err error
    if os.Getenv("OP") == "read" {data, err = k.ReadMonitorEDID()} else {err = k.ApplyMonitorProfile("profile")}
    require((err == nil) == (failure == "ok"), fmt.Sprint("unexpected error: ", err))
    if strings.Contains(failure, "end") {require(strings.Contains(err.Error(), "cannot restore HDMI"), "restore failure lost")}
    if strings.Contains(failure, "command") {require(strings.Contains(err.Error(), "failed"), "utility failure lost")}
    if failure == "begin" {require(strings.Contains(err.Error(), "cannot pause HDMI"), "admission failure lost")}
    if os.Getenv("OP") == "read" {require((len(data) == 256) == (failure == "ok"), "unrestored or incomplete data returned")}
    require(k.HasHDMISignal() == (enabled && !strings.Contains(failure, "end")), "capture state changed")
}
''')
    (out / "native.c").write_text(r'''
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
static unsigned char enabled, previous;
static void record(const char *s) {FILE *f=fopen(getenv("CALLS"),"a"); if(!f) _exit(98); fprintf(f,"%s\n",s); fclose(f);}
int kvmv_hdmi_control(unsigned char value) {record(value ? "control1" : "control0"); enabled=value; return 0;}
unsigned char kvmv_hdmi_signal_active(void) {return enabled;}
int kvmv_edid_maintenance(unsigned char pause) {
    const char *fail=getenv("FAIL");
    record(pause ? "begin" : "end");
    if(pause) {if(!strcmp(fail,"begin")) return -1; previous=enabled; enabled=0;}
    else {if(strstr(fail,"end")) return -1; enabled=previous;}
    return 0;
}
''' + "\n".join("void " + name + "(void) {_exit(99);}" for name in [
        "kvmv_init", "kvmv_deinit", "kvmv_request_keyframe", "kvmv_read_mjpeg_sink",
        "kvmv_read_video_sink_v2", "set_frame_detact", "set_mjpeg_chroma", "get_mjpeg_chroma_status",
    ]))
    subprocess.run(["gcc", "-shared", "-fPIC", str(out / "native.c"), "-o", str(out / "libkvm.so")], check=True)
    env = dict(os.environ, GOTOOLCHAIN="local", CGO_ENABLED="1", CC="gcc",
               CGO_LDFLAGS=f"-L{out} -Wl,-rpath-link,{out}", LD_LIBRARY_PATH=str(out))
    subprocess.run([os.environ.get("GO", "go"), "build", "-tags=nomsgpack", "-overlay", str(overlay),
                    "-o", str(out / "check"), str(out / "main.go")], cwd=root / "server", env=env, check=True)
    cases = 0
    for hardware in ("pcie", "beta"):
        board.write_text(hardware)
        for enabled in ("0", "1"):
            for operation in ("read", "apply"):
                failures = ["ok", "begin", "command", "end", "command-end"]
                if operation == "read":
                    failures.append("size")
                for failure in failures:
                    journal.write_text("")
                    pending.unlink(missing_ok=True)
                    subprocess.run([str(out / "check")], check=True, timeout=10, env=dict(
                        env, CALLS=str(journal), PENDING=str(pending), BOARD=hardware,
                        ENABLED=enabled, OP=operation, FAIL=failure))
                    expected = ["control" + enabled, "begin"]
                    if failure != "begin":
                        expected += ["utility", "end"]
                    assert journal.read_text().splitlines() == expected, (hardware, operation, failure, journal.read_text())
                    assert not list(out.glob("nanokvm-edid-*.bin")), "EDID temporary file leaked"
                    cases += 1
    print(f"PASS {cases} real Go EDID cases: no full capture controls, preserve off/on, begin/utility/end failures, Cube marker and temporary cleanup")
