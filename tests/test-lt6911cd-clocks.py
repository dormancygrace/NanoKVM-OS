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

    def test_module_guard_lifetime_and_unwind(self):
        source = r"""
#include <assert.h>
#include <errno.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#define __init
#define __exit
#define module_param(...)
#define MODULE_PARM_DESC(...)
#define module_param_cb(...)
#define module_init(...)
#define module_exit(...)
#define MODULE_DESCRIPTION(...)
#define MODULE_LICENSE(...)
#define pr_info(...) ((void)0)
#define pr_err(...) ((void)0)
#define ARRAY_SIZE(a) (sizeof(a) / sizeof((a)[0]))
#define ERR_PTR(e) ((struct clk *)(intptr_t)(e))
#define IS_ERR(p) ((uintptr_t)(p) >= (uintptr_t)-4095)
#define IS_ERR_OR_NULL(p) (!(p) || IS_ERR(p))
#define sysfs_emit(buffer, ...) snprintf(buffer, 256, __VA_ARGS__)
enum { CLK_VC_SRC0, CLK_AXI_VIDEO_CODEC, CLK_SRC_VIP_SYS_1,
       CLK_DISPPLL, CLK_CAM1PLL, CLK_MIPIMPLL };
struct kernel_param { int unused; };
struct kernel_param_ops { int (*get)(char *, const struct kernel_param *); };
struct clk { unsigned long rate; struct clk *parent; unsigned div, guards, refs; };
struct device_node { int unused; };
struct of_phandle_args { struct device_node *np; unsigned args_count, args[1]; };
static struct clk hw[6];
static struct device_node provider;
static unsigned gets, fail_get, guard_gets, fail_guard, writes, fail_write;
static bool of_machine_is_compatible(const char *name) {
    return !strcmp(name, "sipeed,nanokvm") || !strcmp(name, "sophgo,sg2002");
}
static struct device_node *of_find_compatible_node(
    struct device_node *from, const char *type, const char *name) {
    assert(!from && !type && !strcmp(name, "sophgo,sg2000-clk"));
    return &provider;
}
static void of_node_put(struct device_node *node) { assert(node == &provider); }
static struct clk *of_clk_get_from_provider(struct of_phandle_args *args) {
    assert(args->np == &provider && args->args_count == 1 && args->args[0] < 6);
    if (++gets == fail_get) return ERR_PTR(-ENOENT);
    hw[args->args[0]].refs++;
    return &hw[args->args[0]];
}
static void clk_put(struct clk *c) { assert(c->refs); c->refs--; }
static int clk_rate_exclusive_get(struct clk *c) {
    assert(!c->parent); // Child protection would recursively pin the PLL too.
    if (++guard_gets == fail_guard) return -EBUSY;
    c->guards++;
    return 0;
}
static void clk_rate_exclusive_put(struct clk *c) { assert(c->guards); c->guards--; }
static struct clk *clk_get_parent(struct clk *c) { return c->parent; }
static unsigned long clk_get_rate(struct clk *c) {
    return c->parent ? (clk_get_rate(c->parent) + c->div - 1) / c->div : c->rate;
}
static long clk_round_rate(struct clk *c, unsigned long r) { assert(c->parent); return r; }
static bool clk_is_match(struct clk *a, struct clk *b) { return a == b; }
static bool fail_mutation(void) {
    for (unsigned i = 3; i < 6; i++) assert(hw[i].guards == 1);
    return ++writes == fail_write;
}
static int clk_set_rate(struct clk *c, unsigned long r) {
    assert(c->parent); // No PLL retuning.
    if (fail_mutation()) return -EIO;
    c->div = (clk_get_rate(c->parent) + r / 2) / r;
    return 0;
}
static int clk_set_parent(struct clk *c, struct clk *parent) {
    if (fail_mutation()) return -EIO;
    c->parent = parent;
    return 0;
}
#include "sg2002-video-clocks.c"
static void reset(unsigned bad_get, unsigned bad_guard, unsigned bad_write) {
    memset(hw, 0, sizeof(hw));
    hw[3].rate = 1188000000; hw[4].rate = 1079999999; hw[5].rate = 900000000;
    hw[0].parent = &hw[3]; hw[0].div = 2;
    hw[1].parent = &hw[5]; hw[1].div = 2;
    hw[2].parent = &hw[3]; hw[2].div = 3;
    gets = guard_gets = writes = held_parents = 0;
    fail_get = bad_get; fail_guard = bad_guard; fail_write = bad_write; ready = false;
    for (unsigned i = 0; i < ARRAY_SIZE(clocks); i++)
        clocks[i].clock = clocks[i].parent = NULL;
}
static void no_resources(void) {
    assert(!held_parents);
    for (unsigned i = 0; i < 6; i++) assert(!hw[i].guards && !hw[i].refs);
}
int main(void) {
    char rates[256];
    for (unsigned model = 0; model < 2; model++) {
        reset(0,0,0); receiver = model ? "d" : "c";
        assert(!video_clocks_init() && ready && writes == 9);
        for (unsigned i = 0; i < 6; i++) assert(!hw[i].guards && hw[i].refs == 1);
        assert(!held_parents); // Nothing keeps a PLL exclusive during capture.
        assert(rates_ops.get(rates, NULL) > 0);
        assert(!strcmp(rates, "396000000 360000000 300000000\n"));
        hw[3].rate += 600;
        rates_ops.get(rates, NULL);
        assert(strcmp(rates, "396000000 360000000 300000000\n")); // Live, not cached.
        video_clocks_exit(); no_resources();
    }
    receiver = "c";
    for (unsigned i = 1; i <= 6; i++) {
        reset(i,0,0); assert(video_clocks_init() == -ENODEV && !ready); no_resources();
    }
    for (unsigned i = 1; i <= 3; i++) {
        reset(0,i,0); assert(video_clocks_init() == -EBUSY && !ready); no_resources();
    }
    for (unsigned i = 1; i <= 9; i++) {
        reset(0,0,i); assert(video_clocks_init() == -EIO && !ready); no_resources();
    }
    reset(0,0,0); receiver = "ux";
    assert(video_clocks_init() == -ENODEV && !gets); no_resources();
    receiver = NULL;
    assert(video_clocks_init() == -ENODEV && !gets); no_resources();
    puts("PASS production module: transition-only guards, live rates, all acquire/apply unwinds");
}
"""
        with tempfile.TemporaryDirectory() as directory:
            tmp = Path(directory)
            for name in ("linux/clk.h", "linux/module.h", "linux/of.h",
                         "dt-bindings/clock/sophgo,cv1800.h"):
                stub = tmp / name
                stub.parent.mkdir(parents=True, exist_ok=True)
                stub.write_text("/* Host CCF fixture declarations precede the production module. */\n")
            c, binary = tmp / "module.c", tmp / "module"
            c.write_text(source)
            flags = ["-fsanitize=address,undefined", "-fno-omit-frame-pointer"] if os.getenv("NANOKVM_SANITIZE") else []
            subprocess.run(["cc", "-std=c11", "-Wall", "-Wextra", "-Werror", *flags,
                            "-I", str(tmp), "-I", str(ROOT / "platform/modules/sg2002-video-clocks"),
                            str(c), "-o", str(binary)], check=True)
            subprocess.run([str(binary)], check=True)

    def test_receiver_gate_and_live_verification(self):
        source = (ROOT / "firmware/alpine/compat/nanokvm-video-clocks").read_text()
        with tempfile.TemporaryDirectory() as directory:
            tmp = Path(directory)
            model, params, calls = tmp / "model", tmp / "params", tmp / "calls"
            policy = tmp / "run/video_clock_policy"
            fixture = source.replace("/etc/kvm/hdmi_version", str(model)).replace(
                "/sys/module/sg2002_video_clocks/parameters", str(params)).replace(
                "/run/nanokvm", str(tmp / "run")).replace(
                "/kvmapp/system/init.d/S15kvmhwd", "detect_model")
            definitions = f"""
pidof() {{ return 1; }}
detect_model() {{ echo detect >> '{calls}'; return 1; }}
modprobe() {{ echo "$*" >> '{calls}'; return 1; }}
"""
            def run(extra=""):
                calls.unlink(missing_ok=True)
                result = subprocess.run(["sh"], input=definitions + extra + fixture,
                                        capture_output=True, text=True)
                # Every exit, including errors, must remove the atomic marker temp.
                self.assertEqual(list(policy.parent.glob("video_clock_policy.*")), [])
                return result

            for name in ("ux", "ue"):
                model.write_text(name)
                self.assertEqual(run().returncode, 0)
                self.assertEqual(policy.read_text().strip(), "dt")
                self.assertFalse(calls.exists())
                self.assertFalse(params.exists())

            # Cold boot NACK/missing cache: retry detection, explicitly defer if
            # still unidentified. No modprobe or live clock change is attempted.
            for name in ("", "unknown", "C", None):
                if name is None:
                    model.unlink()
                else:
                    model.write_text(name)
                self.assertEqual(run().returncode, 0)
                self.assertEqual(calls.read_text().splitlines(), ["detect"])
                self.assertEqual(policy.read_text().strip(), "deferred")
            self.assertNotEqual(run("pidof() { return 0; }\n").returncode, 0)
            self.assertFalse(calls.exists())

            # A real prior apply failure must not be erased by failed detection.
            policy.write_text("failed\n")
            self.assertNotEqual(run().returncode, 0)
            self.assertEqual(policy.read_text().strip(), "failed")
            policy.write_text("deferred\n")
            params.mkdir()
            self.assertNotEqual(run().returncode, 0)
            params.rmdir()

            for name in ("c", "d"):
                loaded = f"""
modprobe() {{
 echo "$*" >> '{calls}'
 mkdir -p '{params}'
 printf '%s\n' '{name}' > '{params}/receiver'
 printf '%s\n' Y > '{params}/ready'
 printf '%s\n' '396000000 360000000 300000000' > '{params}/rates'
}}
"""
                # Late successful S15 detection still applies stock before either
                # service starts, for both C and D.
                model.write_text("")
                late = f"detect_model() {{ echo detect >> '{calls}'; echo {name} > '{model}'; }}\n"
                self.assertEqual(run(late + loaded).returncode, 0)
                self.assertEqual(calls.read_text().splitlines(),
                                 ["detect", f"sg2002-video-clocks receiver={name}"])
                self.assertEqual(policy.read_text().strip(), "stock")
                # Already loaded: verify without a second modprobe, even if busy.
                self.assertEqual(run("pidof() { return 0; }\n").returncode, 0)
                self.assertFalse(calls.exists())
                for receiver, ready, rates in [
                    (name, "Y", "396000000 360000000 300000000"),
                    (name, "N", "396000000 360000000 300000000"),
                    ("d" if name == "c" else "c", "Y", "396000000 360000000 300000000"),
                    (name, "Y", "594000000 450000000 396000000"),
                ]:
                    for n, v in [("receiver", receiver), ("ready", ready), ("rates", rates)]:
                        (params / n).write_text(v + "\n")
                    valid = ready == "Y" and receiver == name and rates.startswith("396")
                    self.assertEqual(run().returncode == 0, valid)
                    self.assertEqual(policy.read_text().strip(), "stock" if valid else "failed")
                for f in params.iterdir():
                    f.unlink()
                params.rmdir()
                self.assertNotEqual(run().returncode, 0)
                self.assertEqual(calls.read_text(), f"sg2002-video-clocks receiver={name}\n")
                self.assertEqual(policy.read_text().strip(), "failed")
                self.assertNotEqual(run("pidof() { return 0; }\n").returncode, 0)
                self.assertFalse(calls.exists())
                # Failed load can recover only through verified stock readback.
                self.assertEqual(run(loaded).returncode, 0)
                self.assertEqual(policy.read_text().strip(), "stock")
                for f in params.iterdir():
                    f.unlink()
                params.rmdir()

    def test_startup_failure_keeps_the_management_services(self):
        source = (ROOT / "kvmapp/system/init.d/S95nanokvm").read_text().split('case "$1" in\n', 1)[0]
        with tempfile.TemporaryDirectory() as directory:
            tmp = Path(directory)
            model, log = tmp / "model", tmp / "log"
            model.write_text("c")
            source = source.replace("/etc/kvm/hdmi_version", str(model)).replace("/run/nanokvm", str(tmp / "run"))
            source = source.replace("/usr/libexec/nanokvm/video-clocks", "clock_gate").replace(
                "/usr/libexec/nanokvm/usb-flags-migrate", "migrate")
            for name, status in ((name, status) for name in ("c", "d", "ux", "ue", "", "unknown") for status in (0, 1)):
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
                                 ["clocks", "start", "start"])
                self.assertEqual("Video clock setup failed" in proc.stderr, status == 1)

    def test_native_capture_guard(self):
        with tempfile.TemporaryDirectory() as directory:
            tmp = Path(directory)
            source = tmp / "guard.cpp"
            source.write_text(r'''#include <cassert>
#include <filesystem>
#include <fstream>
#include "video_clock_policy.hpp"
int main(int argc, char **argv) {
    (void)argc;
    std::string path = std::string(argv[1]) + "/parameters";
    std::string policy = std::string(argv[1]) + "/policy";
    const auto ready = [&](unsigned receiver) {
        return nanokvm::video_clocks_ready(receiver, path, policy);
    };
    assert(ready(1) && ready(2));
    assert(!ready(3) && !ready(0)); // Missing helper/unknown is not verified C.
    std::ofstream(policy) << "deferred\n";
    assert(ready(0) && ready(3)); // Late identification; keep DT until stopped.
    std::ofstream(policy) << "failed\n";
    assert(!ready(0) && !ready(3)); // Never waive a partial profile failure.
    std::ofstream(policy) << "deferred\n";
    std::filesystem::create_directory(path);
    assert(!ready(0) && !ready(3)); // Malformed loaded module cannot be deferred.
    std::ofstream(path + "/receiver") << "c";
    std::ofstream(path + "/ready") << "Y";
    std::ofstream(path + "/rates") << "396000000 360000000 300000000";
    assert(ready(0) && !ready(3));
    std::ofstream(policy) << "stock\n";
    std::ofstream(path + "/receiver") << "d";
    assert(ready(3) && !ready(0));
    std::ofstream(path + "/receiver") << "c";
    std::ofstream(path + "/ready") << "N";
    assert(!ready(0));
    std::ofstream(path + "/ready") << "Y";
    std::ofstream(path + "/rates") << "594000000 450000000 396000000";
    assert(!ready(0)); // Detect rate changes after the transition guard is gone.
    std::filesystem::remove_all(path);
    assert(!ready(0) && !ready(3)); // Module disappeared; stock marker is insufficient.
}''')
            binary = tmp / "guard"
            flags = ["-fsanitize=address,undefined", "-fno-omit-frame-pointer"] if os.getenv("NANOKVM_SANITIZE") else []
            subprocess.run(["g++", "-std=c++17", "-Wall", "-Wextra", "-Werror", *flags,
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
