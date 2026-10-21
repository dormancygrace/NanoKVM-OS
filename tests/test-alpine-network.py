#!/usr/bin/env python3
"""Exercise the Alpine OpenRC network service with isolated interface fixtures."""

import os
from pathlib import Path
import subprocess
import tempfile

SERVICE = Path(__file__).resolve().parents[1] / "firmware/alpine/openrc/nanokvm-network"


def run_case(name, *, eth=False, wifi=False, sdio=False, eth_disabled=False,
             wifi_disabled=False, appears_after_load=False, fail=None,
             expected_calls=(), success=True, missing=None, background=False):
    with tempfile.TemporaryDirectory(prefix="nkos-openrc-net-") as directory:
        root = Path(directory)
        interfaces = root / "net"
        scripts = root / "legacy"
        eth_disabled_file = root / "eth.disabled"
        wifi_config = root / "wifi"
        sdio_devices = root / "sdio"
        interfaces.mkdir()
        scripts.mkdir()
        wifi_config.mkdir()
        sdio_devices.mkdir()
        if eth:
            (interfaces / "eth0").mkdir()
        if wifi:
            (interfaces / "wlan0").mkdir()
        if eth_disabled:
            eth_disabled_file.touch()
        if wifi_disabled:
            (wifi_config / "wifi.disabled").touch()
        if sdio:
            device = sdio_devices / "device:1"
            device.mkdir()
            (device / "modalias").write_text("sdio:c07v5449d0145\n")

        for script in ("S25wifimod", "S30eth", "S30wifi"):
            if script == missing:
                continue
            mock = scripts / script
            mock.write_text(
                "#!/bin/sh\n"
                f"printf '{script} %s\\n' \"$1\" >> \"$CALLS\"\n"
                + (f'mkdir "$NANOKVM_NETWORK_NET_CLASS_DIR/wlan0"\n'
                   if script == "S25wifimod" and appears_after_load else "")
                + ("exit 1\n" if script == fail else "exit 0\n")
            )
            mock.chmod(0o755)

        calls = root / "calls"
        env = dict(os.environ, SERVICE=str(SERVICE), CALLS=str(calls),
                   NANOKVM_NETWORK_NET_CLASS_DIR=str(interfaces),
                   NANOKVM_NETWORK_SDIO_DEVICES_DIR=str(sdio_devices),
                   NANOKVM_NETWORK_LEGACY_DIR=str(scripts),
                   NANOKVM_ETH_DISABLED=str(eth_disabled_file),
                   NANOKVM_WIFI_ETC_DIR=str(wifi_config),
                   NANOKVM_NETWORK_PENDING=str(root / "pending"),
                   LOGGED=str(root / "logged"))
        # The service's start runs the interfaces in the background and always
        # succeeds; the background step is waited for here.
        command = "start; status=$?; wait; exit $status" if background else "start_interfaces"
        result = subprocess.run(
            ["sh", "-c", '. "$SERVICE"; ebegin() { :; }; '
             'eend() { return "$1"; }; ewarn() { printf "%s\\n" "$*" >&2; }; '
             'logger() { printf "%s\\n" "$*" >> "$LOGGED"; }; ' + command],
            env=env, capture_output=True, text=True,
        )
        actual = calls.read_text().splitlines() if calls.exists() else []
        logged = (root / "logged").read_text() if (root / "logged").exists() else ""
        if background:
            assert result.returncode == 0, (name, result)
            assert not (root / "pending").exists(), name
            assert ("No NanoKVM network interface started" in logged) == (not success), (name, logged)
        else:
            assert (result.returncode == 0) == success, (name, result)
        assert actual == list(expected_calls), (name, actual, expected_calls)
        print(f"{name}: pass")


run_case("ethernet only", eth=True,
         expected_calls=("S30eth start",))
run_case("wifi only", wifi=True,
         expected_calls=("S30wifi start",))
run_case("both interfaces", eth=True, wifi=True,
         expected_calls=("S30eth start", "S30wifi start"))
run_case("wifi appears after module load", sdio=True, appears_after_load=True,
         expected_calls=("S25wifimod start", "S30wifi start"))
run_case("no interfaces", success=False,
         expected_calls=())
run_case("sdio without wlan0", sdio=True, success=False,
         expected_calls=("S25wifimod start",))
run_case("ethernet disabled without wifi", eth=True, eth_disabled=True,
         success=False, expected_calls=("S30eth start",))
run_case("wifi disabled without ethernet", wifi=True, wifi_disabled=True,
         success=False, expected_calls=("S30wifi start",))
run_case("ethernet disabled with wifi", eth=True, wifi=True, eth_disabled=True,
         expected_calls=("S30eth start", "S30wifi start"))
run_case("module failure with ethernet", eth=True, sdio=True, fail="S25wifimod",
         expected_calls=("S30eth start", "S25wifimod start"))
run_case("module failure without ethernet", sdio=True, fail="S25wifimod",
         success=False, expected_calls=("S25wifimod start",))
run_case("ethernet failure with wifi", eth=True, wifi=True, fail="S30eth",
         expected_calls=("S30eth start", "S30wifi start"))
run_case("wifi failure with ethernet", eth=True, wifi=True, fail="S30wifi",
         expected_calls=("S30eth start", "S30wifi start"))
run_case("wifi failure alone", wifi=True, fail="S30wifi", success=False,
         expected_calls=("S30wifi start",))
run_case("ethernet failure alone", eth=True, fail="S30eth", success=False,
         expected_calls=("S30eth start",))
run_case("missing wifi script", wifi=True, missing="S30wifi", success=False,
         expected_calls=())
run_case("background start", eth=True, wifi=True, background=True,
         expected_calls=("S30eth start", "S30wifi start"))
run_case("background start without interfaces", background=True, success=False,
         expected_calls=())
run_case("background start, ethernet failure alone", eth=True, fail="S30eth",
         background=True, success=False, expected_calls=("S30eth start",))


def stop_during_start():
    """The timeout must not stop interfaces underneath the background worker."""
    with tempfile.TemporaryDirectory(prefix="nkos-openrc-net-stop-") as directory:
        root = Path(directory)
        calls = root / "calls"
        env = dict(os.environ, SERVICE=str(SERVICE), CALLS=str(calls),
                   NANOKVM_NETWORK_PENDING=str(root / "pending"),
                   RELEASE=str(root / "release"), READY=str(root / "ready"))
        result = subprocess.run(["sh", "-c", r'''
            . "$SERVICE"
            ebegin() { :; }
            eend() { return "$1"; }
            logger() { :; }
            # Accelerate only the service's 150 polling sleeps.
            sleep() { :; }
            start_interfaces() {
                : > "$READY"
                while [ ! -e "$RELEASE" ]; do command sleep 0.01; done
                echo started >> "$CALLS"
            }
            stop_legacy() { echo "$1 stop" >> "$CALLS"; }
            start || exit 10
            while [ ! -e "$READY" ]; do command sleep 0.01; done
            stop
            status=$?
            [ "$status" -ne 0 ] && [ -e "$NANOKVM_NETWORK_PENDING" ] &&
                [ ! -e "$CALLS" ]
            safe=$?
            : > "$RELEASE"
            wait
            [ "$safe" -eq 0 ] || exit 11
            [ ! -e "$NANOKVM_NETWORK_PENDING" ] || exit 12
            stop || exit 13
        '''], env=env, capture_output=True, text=True, timeout=10)
        assert result.returncode == 0, result
        assert calls.read_text().splitlines() == [
            "started", "S30wifi stop", "S30eth stop"], calls.read_text()
        print("stop timeout preserves worker; retry stops interfaces in order: pass")


stop_during_start()
