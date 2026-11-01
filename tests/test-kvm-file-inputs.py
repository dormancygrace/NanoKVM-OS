#!/usr/bin/env python3
"""Compile and run the native FileStamp test; no camera SDK or device needed.

Optional CXXFLAGS can enable sanitizers.
"""
from pathlib import Path
import os
import shlex
import subprocess
import tempfile

repo = Path(__file__).resolve().parents[1]
with tempfile.TemporaryDirectory(prefix="kvm-file-inputs-") as directory:
    binary = Path(directory) / "test"
    subprocess.run([
        *shlex.split(os.environ.get("CXX", "g++")),
        "-std=gnu++17", "-Wall", "-Wextra", "-Werror", "-O1", "-g",
        *shlex.split(os.environ.get("CXXFLAGS", "")),
        "-I" + str(repo / "support/additional/kvm/include"),
        str(repo / "tests/test-kvm-file-inputs.cpp"), "-o", str(binary),
    ], check=True)
    subprocess.run([str(binary)], check=True)
