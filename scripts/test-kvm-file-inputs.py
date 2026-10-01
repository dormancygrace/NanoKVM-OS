#!/usr/bin/env python3
"""Compile/run native file-input regressions without the camera SDK or device.

The harness uses the real HDMI mode and watchdog-check functions, with only
paths, the clock and fopen replaced. Optional CXXFLAGS can enable sanitizers.
"""
from pathlib import Path
import os
import shlex
import subprocess
import tempfile

repo = Path(__file__).resolve().parents[1]
source = (repo / "support/sg2002/additional/kvm/src/kvm_vision.cpp").read_text()


def function(signature):
    start = source.index(signature)
    opening = source.index("{", start)
    depth = 0
    for position in range(opening, len(source)):
        if source[position] == "{":
            depth += 1
        elif source[position] == "}":
            depth -= 1
            if depth == 0:
                return source[start : position + 1]
    raise ValueError("Unterminated function: " + signature)


prefix = r'''#include "internal/file_stamp.hpp"
#include "internal/ion_summary.hpp"
#include "internal/small_file.hpp"
#include <cstdarg>
#include <cstdlib>
#include <string>
#include <cstdio>
#include <cerrno>
#include <cstdint>
#include <unistd.h>
static const char *hdmi_mode_path;
static const char *ion_summary_path;
static constexpr uint32_t hdmi_mode_max_age_ms = 1000U;
static nanokvm::FileStamp hdmi_mode_stamp;
static struct { uint8_t hdmi_mode = 0; } kvmv_cfg;
static uint32_t fake_now_ms = 0;
namespace vi_state_shared {
uint32_t monotonic_ms() { return fake_now_ms; }
}
static unsigned mode_open_count = 0;
static bool fail_mode_open = false;
static FILE *mode_fopen(const char *path, const char *mode) {
    ++mode_open_count;
    if (fail_mode_open) { errno = EACCES; return nullptr; }
    return std::fopen(path, mode);
}
static void debug(const char *, ...) {}
#define fopen mode_fopen
'''
functions = "\n\n".join(function(signature) for signature in (
    "int set_hdmi_mode(uint8_t _hdmi_mode)",
    "int get_hdmi_mode(void)",
    "uint8_t chack_ion()",
))
harness = (repo / "scripts/test-kvm-file-inputs.cpp").read_text()
with tempfile.TemporaryDirectory(prefix="kvm-file-inputs-") as directory:
    directory = Path(directory)
    translation_unit = directory / "test.cpp"
    translation_unit.write_text(prefix + functions + "\n#undef fopen\n" + harness)
    binary = directory / "test"
    compiler = shlex.split(os.environ.get("CXX", "g++"))
    subprocess.run([
        *compiler, "-std=gnu++17", "-Wall", "-Wextra", "-Werror", "-O1", "-g",
        *shlex.split(os.environ.get("CXXFLAGS", "")),
        "-I" + str(repo / "support/sg2002/additional/kvm/include"),
        str(translation_unit), "-o", str(binary),
    ], check=True)
    subprocess.run([str(binary), str(directory)], check=True)
