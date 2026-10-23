#!/usr/bin/env python3
"""Execute the production C/D clock transition and startup gate with fault injection."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class ClockPolicyTests(unittest.TestCase):
    def test_ccf_transitions_and_failures(self):
        source = r'''
#include <assert.h>
#include <errno.h>
#include <stdbool.h>
#include <stdio.h>
struct clk { unsigned long rate; struct clk *parent; unsigned div; };
static int writes, fail_at, bad_at, rounding;
static struct clk *clk_get_parent(struct clk *c) { return c->parent; }
static unsigned long clk_get_rate(struct clk *c) {
    return c->parent ? (clk_get_rate(c->parent) + c->div - 1) / c->div : c->rate;
}
static long clk_round_rate(struct clk *c, unsigned long r) {
    (void)c; return rounding ? (long)r - 1 : (long)r;
}
static bool clk_is_match(struct clk *a, struct clk *b) { return a == b; }
static int clk_set_rate(struct clk *c, unsigned long r) {
    writes++;
    if (writes == fail_at) return -EIO;
    if (writes != bad_at) c->div = (clk_get_rate(c->parent) + r / 2) / r;
    return 0;
}
static int clk_set_parent(struct clk *c, struct clk *p) {
    writes++;
    if (writes == fail_at) return -EIO;
    if (writes != bad_at) c->parent = p;
    /* No transition may temporarily exceed this clock's stock target. */
    unsigned long limit = p->rate >= 1079999999 && p->rate <= 1080000001 ? 360000000 :
                          p->rate == 900000000 ? 300000000 : 396000000;
    assert(clk_get_rate(c) <= limit);
    return 0;
}
#include "profile.h"
static int run(int fail, int bad, int pll_bad, int round_bad) {
    struct clk disp = {1188000000,0,0}, cam = {1080000000,0,0},
               mipi = {900000000,0,0};
    struct clk vc = {0,&disp,2}, axi = {0,&mipi,2}, vip = {0,&disp,3};
    struct video_clock c[] = {
        {&vc,&disp,396000000,1188000000},
        {&axi,&cam,360000000,1080000000},
        {&vip,&mipi,300000000,900000000},
    };
    if (pll_bad == 1) cam.rate = 1030000000;
    if (pll_bad >= 2) cam.rate--;
    if (pll_bad == 3) { vc.div = 3; axi.parent = &cam; axi.div = 3; vip.parent = &mipi; vip.div = 3; }
    writes = 0; fail_at = fail; bad_at = bad; rounding = round_bad;
    int ret = apply_cd_stock(c, 3);
    if (!ret) {
        assert(clk_get_rate(&vc) == 396000000);
        assert(clk_get_rate(&axi) == 360000000);
        assert(clk_get_rate(&vip) == 300000000);
    }
    assert(disp.rate == 1188000000 && mipi.rate == 900000000);
    assert(cam.rate == (pll_bad == 1 ? 1030000000UL : pll_bad >= 2 ? 1079999999UL : 1080000000UL));
    return ret;
}
int main(void) {
    assert(!run(0,0,0,0) && writes == 9);
    assert(!run(0,0,2,0) && writes == 9);
    assert(!run(0,0,3,0) && writes == 9);
    for (int i=1; i<=9; i++) assert(run(i,0,0,0) == -EIO);
    /* A parent already matching (step 2) is a legitimate no-op. */
    for (int i=1; i<=9; i++) if (i != 2) assert(run(0,i,0,0));
    assert(run(0,0,1,0) == -EINVAL && writes == 0);
    assert(run(0,0,0,1) == -EINVAL && writes == 0);
    puts("PASS production CCF profile: exact rates, all writes, readback, PLL and rounding failures");
}
'''
        with tempfile.TemporaryDirectory() as directory:
            tmp = Path(directory)
            c, binary = tmp / "test.c", tmp / "test"
            c.write_text(source)
            flags = ["-fsanitize=address,undefined", "-fno-omit-frame-pointer"] if os.getenv("NANOKVM_SANITIZE") else []
            subprocess.run(["cc", "-std=c11", "-Wall", "-Wextra", "-Werror", *flags,
                            "-I", str(ROOT / "platform/modules/sg2002-video-clocks"),
                            str(c), "-o", str(binary)], check=True)
            subprocess.run([str(binary)], check=True)

    def test_receiver_gate_and_live_verification(self):
        source = (ROOT / "firmware/alpine/compat/nanokvm-video-clocks").read_text()
        with tempfile.TemporaryDirectory() as directory:
            tmp = Path(directory)
            model, params, calls = tmp / "model", tmp / "params", tmp / "calls"
            params.mkdir()
            fixture = source.replace("/etc/kvm/hdmi_version", str(model)).replace(
                "/sys/module/sg2002_video_clocks/parameters", str(params))
            definitions = f"""
pidof() {{ return 1; }}
modprobe() {{ echo "$*" >> '{calls}'; return 1; }}
"""
            def run():
                return subprocess.run(["sh"], input=definitions + fixture,
                                      capture_output=True, text=True)
            for name in ("ux", "ue"):
                model.write_text(name)
                self.assertEqual(run().returncode, 0)
                self.assertFalse(calls.exists())
            for name in ("", "unknown", "C"):
                model.write_text(name)
                self.assertNotEqual(run().returncode, 0)
                self.assertFalse(calls.exists())
            model.unlink()
            self.assertNotEqual(run().returncode, 0)
            for name in ("c", "d"):
                model.write_text(name)
                for receiver, ready, rates in [
                    (name, "Y", "396000000 360000000 300000000"),
                    (name, "N", "396000000 360000000 300000000"),
                    ("d" if name == "c" else "c", "Y", "396000000 360000000 300000000"),
                    (name, "Y", "594000000 450000000 396000000"),
                ]:
                    for n, v in [("receiver", receiver), ("ready", ready), ("rates", rates)]:
                        (params / n).write_text(v + "\n")
                    self.assertEqual(run().returncode == 0,
                                     ready == "Y" and receiver == name and rates.startswith("396"))
                for f in params.iterdir():
                    f.unlink()
                params.rmdir()
                self.assertNotEqual(run().returncode, 0)
                self.assertEqual(calls.read_text(), f"sg2002-video-clocks receiver={name}\n")
                calls.unlink()
                busy = definitions + "\npidof() { return 0; }\n"
                self.assertNotEqual(subprocess.run(["sh"], input=busy + fixture,
                    capture_output=True, text=True).returncode, 0)
                self.assertFalse(calls.exists())
                # A successful load must publish matching, live verified rates.
                loaded = definitions + f"""
modprobe() {{
 echo "$*" >> '{calls}'
 mkdir -p '{params}'
 printf '%s\n' '{name}' > '{params}/receiver'
 printf '%s\n' Y > '{params}/ready'
 printf '%s\n' '396000000 360000000 300000000' > '{params}/rates'
}}
"""
                self.assertEqual(subprocess.run(["sh"], input=loaded + fixture,
                    capture_output=True, text=True).returncode, 0)
                calls.unlink()
                # Already loaded: verify without a second modprobe, even if busy.
                self.assertEqual(subprocess.run(["sh"], input=busy + fixture,
                    capture_output=True, text=True).returncode, 0)
                self.assertFalse(calls.exists())

    def test_startup_failure_keeps_the_management_services(self):
        source = (ROOT / "kvmapp/system/init.d/S95nanokvm").read_text().split('case "$1" in\n', 1)[0]
        with tempfile.TemporaryDirectory() as directory:
            tmp = Path(directory)
            model, log = tmp / "model", tmp / "log"
            model.write_text("c")
            source = source.replace("/etc/kvm/hdmi_version", str(model)).replace("/run/nanokvm", str(tmp / "run"))
            source = source.replace("/usr/libexec/nanokvm/video-clocks", "clock_gate").replace(
                "/usr/libexec/nanokvm/usb-flags-migrate", "migrate")
            for name, status in ((name, status) for name in ("c", "d", "ux", "ue") for status in (0, 1)):
                model.write_text(name)
                log.unlink(missing_ok=True)
                fixture = source + f'''
pidof() {{ return 1; }}
migrate() {{ return 0; }}
prepare_hdmi_receiver() {{ return 0; }}
clock_gate() {{ echo clocks >> '{log}'; return {status}; }}
video_settings() {{ :; }}
remove_legacy_links() {{ :; }}
start_background() {{ echo start >> '{log}'; }}
start_services
'''
                proc = subprocess.run(["sh"], input=fixture, text=True, capture_output=True)
                self.assertEqual(proc.returncode, 0, proc.stderr)
                self.assertEqual(log.read_text().splitlines(),
                                 (["clocks"] if name in ("c", "d") else []) + ["start", "start"])
                self.assertEqual("Starting without video" in proc.stderr, status == 1 and name in ("c", "d"))

    def test_native_capture_guard(self):
        with tempfile.TemporaryDirectory() as directory:
            tmp = Path(directory)
            source = tmp / "guard.cpp"
            source.write_text('''#include <cassert>
#include <fstream>
#include "video_clock_policy.hpp"
int main(int argc, char **argv) {
    (void)argc;
    std::string path = argv[1];
    assert(nanokvm::video_clocks_ready(1, path));
    assert(nanokvm::video_clocks_ready(2, path));
    assert(!nanokvm::video_clocks_ready(3, path));
    assert(!nanokvm::video_clocks_ready(0, path));
    std::ofstream(path + "/receiver") << "c";
    std::ofstream(path + "/ready") << "Y";
    std::ofstream(path + "/rates") << "396000000 360000000 300000000";
    assert(nanokvm::video_clocks_ready(0, path));
    assert(!nanokvm::video_clocks_ready(3, path));
    std::ofstream(path + "/receiver") << "d";
    assert(nanokvm::video_clocks_ready(3, path));
    assert(!nanokvm::video_clocks_ready(0, path));
    std::ofstream(path + "/receiver") << "c";
    std::ofstream(path + "/rates") << "594000000 450000000 396000000";
    assert(!nanokvm::video_clocks_ready(0, path));
}''')
            binary = tmp / "guard"
            subprocess.run(["g++", "-std=c++17", "-Wall", "-Wextra", "-Werror",
                            "-I", str(ROOT / "support/sg2002/additional/kvm/include"),
                            str(source), "-o", str(binary)], check=True)
            subprocess.run([str(binary), str(tmp)], check=True)
        vision = (ROOT / "support/sg2002/additional/kvm/src/kvm_vision.cpp").read_text()
        restart = vision[vision.index("static bool restart_capture_now()"):vision.index("// Caller holds vi_mutex")]
        self.assertLess(restart.index("video_clocks_ready"), restart.index("cam->restart"))

    def test_packaged_and_built(self):
        build = (ROOT / "platform/build.sh").read_text()
        self.assertIn('build "$src/video-clocks"', build)
        self.assertIn('"$src/video-clocks" "$src/cryptodev"', build)
        self.assertIn("firmware/alpine/compat/nanokvm-video-clocks", (ROOT / "platform/packages.list").read_text())
        for name, capability in [("nanokvm-base", "nanokvm-video-clock-policy=1"),
                                 ("nanokvm-kmod-sg2002", "nanokvm-lt6911cd-stock-clocks=1")]:
            self.assertIn(capability, (ROOT / f"firmware/alpine/packages/{name}/APKBUILD").read_text())
            self.assertIn(capability, (ROOT / "firmware/alpine/packages/nanokvm/APKBUILD").read_text())
        self.assertIn("<594000000>, <450000000>, <396000000>", (ROOT / "firmware/boards/sg2002-nanokvm-common.dtsi").read_text())


if __name__ == "__main__":
    unittest.main()
