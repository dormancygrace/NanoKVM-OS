#!/usr/bin/env python3
"""Exercise virtual-console migration and USB watcher lifecycle hooks."""
from pathlib import Path
import os
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]

with tempfile.TemporaryDirectory() as temp:
    d = Path(temp)
    inittab = d / "inittab"
    preserved = "# custom consoles\nttyS0::respawn:/sbin/getty -L 115200 ttyS0 vt100\ntty2::respawn:/sbin/getty 9600 tty2\ntty3::askfirst:/sbin/getty 38400 tty3\n::shutdown:/sbin/openrc shutdown\n"
    inittab.write_text(preserved + "".join(f"tty{i}::respawn:/sbin/getty 38400 tty{i}\n" for i in range(1, 7)))
    inittab.chmod(0o640)
    policy = ROOT / "firmware/alpine/compat/nanokvm-console-policy"
    for _ in range(2):
        subprocess.run(["sh", str(policy), str(inittab)], check=True)
        assert inittab.read_text() == preserved
        assert inittab.stat().st_mode & 0o777 == 0o640

    # Exercise OpenRC service functions with a simulated supervisor. A signal-0
    # probe is supported by the device's OpenRC (there is no --status option).
    service = ROOT / "firmware/alpine/openrc/nanokvm-usb-internet"
    helper = d / "helper"
    helper.write_text('#!/bin/sh\necho call >> "$CALLS"\n[ -e "$NEEDED" ]\n')
    helper.chmod(0o755)
    config = d / "c.1"
    config.mkdir()
    env = dict(os.environ, NEEDED=str(d/"needed"), HELPER=str(helper), SERVICE=str(service),
               NANOKVM_USB_NET_CONFIG=str(config), CALLS=str(d/"calls"))
    # Without a bound NCM or RNDIS function the helper is not even asked.
    subprocess.run(["bash", "-eu", "-c", r'''
        . "$SERVICE"
        command=$HELPER
        RC_SVCNAME=fixture
        supervise-daemon() { [ "$2" = --signal ] && return 1; exit 90; }
        touch "$NEEDED"
        reload
        [ ! -e "$CALLS" ]
        rm "$NEEDED"
    '''], env=env, check=True)
    (config / "ncm.usb0").symlink_to("../../functions/ncm.usb0")
    subprocess.run(["bash", "-eu", "-c", r'''
        . "$SERVICE"
        command=$HELPER
        RC_SVCNAME=fixture
        running=0; starts=0; stops=0
        service_get_value() { :; }
        supervise-daemon() {
            case "$2" in
                --signal) [ "$3" = 0 ]; [ "$running" = 1 ] ;;
                --start) running=1; starts=$((starts+1)) ;;
                --stop) running=0; stops=$((stops+1)) ;;
                *) exit 90 ;;
            esac
        }
        start; [ "$running:$starts:$stops" = 0:0:0 ]
        touch "$NEEDED"
        reload; reload; [ "$running:$starts:$stops" = 1:1:0 ]
        rm "$NEEDED"
        reload; reload; [ "$running:$starts:$stops" = 0:1:1 ]
        touch "$NEEDED"
        start; stop; [ "$running:$starts:$stops" = 0:2:2 ]
    '''], env=env, check=True)

    # Real S30usbnet must notify its supervisor outside the flock, while calls
    # from the watcher itself skip notification (no self-stop/deadlock).
    helper.write_text('#!/bin/sh\nexit 0\n')
    watcher = d / "watch-service"
    watcher.write_text('#!/bin/sh\nflock -n "$NANOKVM_USB_NET_RUN/lock" true || exit 91\necho "$1" >> "$WATCH_LOG"\n')
    watcher.chmod(0o755)
    boot=d/"boot";boot.mkdir()
    (boot/"rndis.ipv4_prefix").write_text("10.22.33\n")
    env.update(NANOKVM_USB_NET_BOOT=str(boot),NANOKVM_USB_NET_RUN=str(d/"run"),NANOKVM_USB_NET_PID=str(d/"legacy.pid"),NANOKVM_USB_INTERNET_HELPER=str(helper),NANOKVM_USB_WATCH_SERVICE=str(watcher),WATCH_LOG=str(d/"watch.log"))
    script=ROOT/"firmware/alpine/compat/S30usbnet"
    for action in ["start","stop","restart"]:
        subprocess.run(["sh",str(script),action],env=env,check=True)
    assert (d/"watch.log").read_text()=="reload\nstop\nstop\nreload\n"
    env["NANOKVM_USB_NET_WATCHER"]="1"
    subprocess.run(["sh",str(script),"start"],env=env,check=True)
    assert (d/"watch.log").read_text()=="reload\nstop\nstop\nreload\n"
print("PASS: console migration, watcher lifecycle/idempotence, lock release and recursion guard")
