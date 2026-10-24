#!/usr/bin/env python3
"""Compile every board DT and verify that HDMI INT remains a userspace input.

Requires a prepared Linux source tree and cpp/dtc/fdtget on PATH; no device.
"""
import argparse
from pathlib import Path
import subprocess
import tempfile

root = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--kernel-source", type=Path, required=True)
args = parser.parse_args()
kernel = args.kernel_source.resolve()


def run(command):
    return subprocess.run(command, check=True, text=True, capture_output=True).stdout


with tempfile.TemporaryDirectory(prefix="nanokvm-hdmi-int-dt-") as directory:
    for profile in ("alpha", "beta", "pcie", "lite", "detect"):
        work = Path(directory)
        source, binary = work / (profile + ".dts"), work / (profile + ".dtb")
        source.write_text(run([
            "cpp", "-P", "-nostdinc", "-undef", "-D__DTS__", "-x", "assembler-with-cpp",
            "-I" + str(kernel / "arch/riscv/boot/dts/sophgo"),
            "-I" + str(kernel / "include"),
            "-I" + str(kernel / "scripts/dtc/include-prefixes"),
            str(root / "firmware/boards" / ("sg2002-nanokvm-" + profile + ".dts")),
        ]))
        run(["dtc", "-q", "-I", "dts", "-O", "dtb", "-o", str(binary), str(source)])

        def get(node, prop, kind="x"):
            return run(["fdtget", "-t", kind, str(binary), node, prop]).strip()

        def cells(node, prop):
            return [int(value, 16) for value in get(node, prop).split()]

        gpio = "/soc/gpio@5021000"
        config = "/soc/pinctrl@3001000/nanokvm-hdmi-int-cfg"
        assert get(gpio, "pinctrl-names", "s") == "default"
        references = cells(gpio, "pinctrl-0")
        assert cells(config, "phandle")[0] in references, profile
        pin = config + "/hdmi-int-pins"
        # SG2002 binding pin48 / function0 / no secondary mux = PWR_GPIO1.
        assert cells(pin, "pinmux") == [0xFF000030], profile
        assert cells(pin, "power-source") == [1800]
        assert set(run(["fdtget", "-p", str(binary), pin]).split()) == {
            "pinmux", "power-source"
        }, "INT must not configure output, pull, or drive strength"

        # A variant must retain its other GPIO-bank pin ownership.
        other = {"pcie": "nanokvm-hdd-led-cfg", "alpha": "nanokvm-alpha-reset-cfg",
                 "detect": "nanokvm-alpha-reset-cfg"}.get(profile)
        if other:
            node = "/soc/pinctrl@3001000/" + other
            assert cells(node, "phandle")[0] in references, profile
        port = gpio + "/gpio-controller@0"
        for child in run(["fdtget", "-l", str(binary), port]).split():
            properties = run(["fdtget", "-p", str(binary), port + "/" + child]).split()
            if "gpio-hog" in properties:
                values = cells(port + "/" + child, "gpios")
                assert 1 not in values[::2], "HDMI INT must remain available to GPIO v2"
        print("PASS " + profile + ": GPIOE1 default mux, 1.8V, no output/hog; other bank pins retained")
