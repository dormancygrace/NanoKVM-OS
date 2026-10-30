#!/usr/bin/env python3
"""Real BusyBox/OpenRC -> RAM PID1, isolated in QEMU (never the host PID1).

Requires an Alpine x86_64 root and matching netboot kernel/initramfs expanded
under OUTPUT/qemu/{root,initrd}, as described in docs/safe-poweroff.md.
Every disk created here is a disposable regular file, never a host block device.
"""
import argparse
import json
import os
from pathlib import Path
import re
import socket
import subprocess
import time
import urllib.request
import urllib.error

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("output", type=Path)
parser.add_argument("--mode", choices=["web", "magic", "held-mount", "hidden-mount", "dhcp", "stop-error"], default="web")
parser.add_argument("--filesystem",choices=["ext4","f2fs"],default="f2fs")
args = parser.parse_args()
out = args.output.resolve()
q = out / "qemu"
root = q / "root"
initrd = q / "initrd"
repo = Path(__file__).resolve().parents[2]
build_env=dict(os.environ,CGO_ENABLED="0",GOOS="linux",GOARCH="amd64")
for target, source in [("nkos-standby","./cmd/nkos-standby"),("standby-test-driver","../tests/standby/driver.go")]:
    subprocess.run(["go","build","-trimpath","-ldflags=-s -w","-o",str(out/target),source],
                   cwd=repo/"server",env=build_env,check=True)
assert root.is_dir() and initrd.is_dir(), "prepare the documented Alpine fixture"
assert not out.is_block_device()
def put(path, text, mode=0o644):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)
    path.chmod(mode)
def link(path, target):
    if path.is_symlink():
        path.unlink()
    elif path.exists():
        raise RuntimeError(f"refusing to replace {path}")
    path.symlink_to(target)
subprocess.run(["cp", str(out / "nkos-standby"), str(root / "usr/sbin/nkos-standby")], check=True)
subprocess.run(["cp", str(out / "standby-test-driver"), str(root / "test-driver")], check=True)
put(root/"etc/inittab", """::sysinit:/sbin/openrc sysinit
::sysinit:/sbin/openrc boot
::wait:/sbin/openrc default
::shutdown:/sbin/openrc shutdown
""")
put(root/"etc/rc.conf", 'rc_sys=""\nrc_logger="NO"\n')
put(root/"etc/fstab", f"/dev/mmcblk0p2 / {args.filesystem} defaults 0 0\n/dev/vdb /boot vfat defaults 0 0\n/dev/vdc /data exfat defaults 0 0\n")
put(root/"etc/hostname", "standby-test\n")
put(root/"boot/eth.nodhcp", "10.0.2.15/24 10.0.2.2\n")
loopimage=root/"loop.img"
with loopimage.open("wb") as f: f.truncate(8*1024*1024)
subprocess.run(["mkfs.vfat",str(loopimage)],stdout=subprocess.DEVNULL,check=True)
put(root/"test-once", "")
put(root/"test-mode", args.mode if args.mode in ("held-mount","hidden-mount") else "")
put(root/"kvmapp/.os-update/lock", "")
put(root/"lib/apk/db/lock", "")
put(root/"etc/init.d/test-app", """#!/sbin/openrc-run
depend() { need test-net test-writer; }
start() { /test-driver & echo $! >/run/test-app.pid; }
stop() { kill "$(cat /run/test-app.pid)"; sleep 0.5; }
""",0o755)
put(root/"etc/init.d/test-net", """#!/sbin/openrc-run
depend() { need test-storage; }
start() {
  ip link set eth0 up
""" + ("""  udhcpc -i eth0 -p /run/udhcpc.eth0.pid -s /etc/test-dhcp -q
  udhcpc -i eth0 -p /run/udhcpc.eth0.pid -s /etc/test-dhcp -b
""" if args.mode=="dhcp" else """  ip addr add 10.0.2.15/24 dev eth0
  ip route add default via 10.0.2.2
""") + """}
stop() {
  [ ! -f /run/udhcpc.eth0.pid ] || kill "$(cat /run/udhcpc.eth0.pid)"
  ip addr flush dev eth0
  ip link set eth0 down
}
""", 0o755)
put(root/"etc/test-dhcp", """#!/bin/sh
case "$1" in
bound|renew)
  ip addr replace "$ip/24" dev eth0
  ip route replace default via "$router"
  ;;
esac
""",0o755)
put(root/"writer", """#!/bin/sh
trap 'for p in /writer.log /boot/writer.log /data/writer.log /m/writer.log; do echo stopped >> "$p"; done; sync; exit 0' TERM
while :; do
  for p in /writer.log /boot/writer.log /data/writer.log /m/writer.log; do echo writing >> "$p"; done
  sleep 0.2
done
""",0o755)
put(root/"etc/init.d/test-storage", """#!/sbin/openrc-run
depend() { need localmount; }
start() {
  [ -e /data/loop.img ] || cp /loop.img /data/loop.img
  mkdir -p /m
  mount -o loop /data/loop.img /m
}
stop() { :; }
""",0o755)
put(root/"etc/init.d/test-writer", """#!/sbin/openrc-run
depend() { need test-net; }
start() {
  echo 8388608 >/sys/block/zram0/disksize
  mkswap /dev/zram0 >/dev/null
  swapon /dev/zram0
  /writer & echo $! >/run/test-writer.pid
}
stop() { kill "$(cat /run/test-writer.pid)"; sleep 1; RETURN_FAULT }
""".replace("RETURN_FAULT","return 1;" if args.mode=="stop-error" else ""),0o755)
for level, services in {
    "sysinit": ["devfs","procfs","sysfs"],
    "boot": ["bootmisc","hostname","localmount"],
    "default":["test-storage","test-net","test-writer","test-app"],
    "shutdown":["mount-ro","savecache"],
}.items():
    for name in services:
        path=root/f"etc/runlevels/{level}/{name}"
        path.parent.mkdir(parents=True,exist_ok=True)
        if not path.exists(): link(path,"/etc/init.d/"+name)
put(initrd/"init", """#!/bin/sh
export PATH=/sbin:/bin:/usr/sbin:/usr/bin
mount -t devtmpfs devtmpfs /dev
mount -t proc proc /proc
mount -t sysfs sysfs /sys
modprobe virtio_pci
modprobe virtio_blk
modprobe virtio_net
modprobe ext4
modprobe f2fs
modprobe vfat
modprobe nls_cp437
modprobe nls_iso8859_1
modprobe nls_utf8
modprobe exfat
modprobe af_packet
modprobe loop
modprobe zram
for i in $(seq 1 100); do [ -b /dev/vda ] && break; sleep 0.1; done
ln -s vda /dev/mmcblk0p2
mkdir -p /sysroot
mount -t ROOT_FS /dev/mmcblk0p2 /sysroot || exec sh
mkdir -p /sysroot/dev /sysroot/proc /sysroot/sys /sysroot/run
mount -t tmpfs tmpfs /sysroot/run
mount --move /dev /sysroot/dev
mount --move /proc /sysroot/proc
mount --move /sys /sysroot/sys
exec switch_root /sysroot /sbin/init
""".replace("ROOT_FS",args.filesystem),0o755)
archive=q/"test-initramfs.gz"
with archive.open("wb") as f:
    subprocess.run(["sh","-c","find . -print0 | cpio --null -o -H newc --quiet | gzip -1"],cwd=initrd,stdout=f,check=True)
disk=q/"disk.img"
with disk.open("wb") as f: f.truncate(128*1024*1024)
if args.filesystem=="ext4":
    subprocess.run(["mkfs.ext4","-q","-F","-d",str(root),str(disk)],check=True)
else:
    subprocess.run(["mkfs.f2fs","-f",str(disk)],stdout=subprocess.DEVNULL,check=True)
    subprocess.run(["sload.f2fs","-f",str(root),str(disk)],stdout=subprocess.DEVNULL,check=True)
boot=q/"boot.img"
data=q/"data.img"
for path,size in [(boot,16*1024*1024),(data,32*1024*1024)]:
    with path.open("wb") as f:f.truncate(size)
subprocess.run(["mkfs.vfat",str(boot)],stdout=subprocess.DEVNULL,check=True)
subprocess.run(["mcopy","-i",str(boot),str(root/"boot/eth.nodhcp"),"::eth.nodhcp"],check=True)
subprocess.run(["mkfs.exfat",str(data)],stdout=subprocess.DEVNULL,check=True)
qmp=q/"qmp.sock"
qmp.unlink(missing_ok=True)
log=q/"console.log"
cmd=["qemu-system-x86_64","-m","256","-nographic","-no-reboot",
     "-kernel",str(q/"vmlinuz-virt"),"-initrd",str(archive),
     "-append","console=ttyS0 root=/dev/mmcblk0p2 quiet panic=-1",
     "-drive",f"file={disk},if=virtio,format=raw,cache=none",
     "-drive",f"file={boot},if=virtio,format=raw,cache=none",
     "-drive",f"file={data},if=virtio,format=raw,cache=none",
     "-netdev","user,id=n0,hostfwd=tcp:127.0.0.1:18081-:80,hostfwd=udp:127.0.0.1:19009-:9",
     "-device","virtio-net-pci,netdev=n0,mac=52:54:00:12:34:56",
     "-qmp",f"unix:{qmp},server=on,wait=off"]
def get(path,token,post=False):
    headers={"Authorization":"Bearer "+token}
    if post:headers["Origin"]="http://127.0.0.1:18081"
    req=urllib.request.Request("http://127.0.0.1:18081"+path,headers=headers,method="POST" if post else "GET",data=b"" if post else None)
    with urllib.request.urlopen(req,timeout=3) as rsp:return rsp.read()
with log.open("wb") as stream:
    vm=subprocess.Popen(cmd,stdin=subprocess.DEVNULL,stdout=stream,stderr=subprocess.STDOUT)
    try:
        token=None
        deadline=time.monotonic()+90
        while time.monotonic()<deadline:
            text=log.read_text(errors="replace")
            assert vm.poll() is None,text
            if "STANDBY_TEST_ERROR" in text:raise AssertionError(text)
            match=re.search(r"STANDBY_TEST_TOKEN ([0-9a-f]{64})",text)
            if match:token=match[1];break
            time.sleep(.2)
        assert token, log.read_text(errors="replace")
        assert "STANDBY_TEST_PREFLIGHT_REFUSALS" in log.read_text(errors="replace")
        status=None
        while time.monotonic()<deadline:
            try:
                status=json.loads(get("/standby/status",token))
                if status["state"] in ("ready","failed"):break
            except (OSError,ValueError,urllib.error.URLError):pass
            time.sleep(.25)
        assert status,log.read_text(errors="replace")
        expected="failed" if args.mode in ("held-mount","hidden-mount","stop-error") else "ready"
        assert status["state"]==expected, (status,log.read_text(errors="replace"))
        assert status["safe"]==(expected=="ready"),status
        # An independent observation from the emulated disk, outside the guest.
        sock=socket.socket(socket.AF_UNIX);sock.connect(str(qmp));fp=sock.makefile("rwb",buffering=0)
        fp.readline()
        def query(command):
            fp.write(json.dumps({"execute":command}).encode()+b"\n")
            while True:
                reply=json.loads(fp.readline())
                if "return" in reply:return reply["return"]
                if "error" in reply:raise AssertionError(reply)
        query("qmp_capabilities")
        before=[d["stats"] for d in query("query-blockstats")]
        for _ in range(10):
            s=json.loads(get("/standby/status",token))
            assert s["state"]==expected,s
            time.sleep(.5)
        after=[d["stats"] for d in query("query-blockstats")]
        writes=["rd_bytes","rd_operations","wr_bytes","wr_operations","flush_operations"]
        if expected=="ready":
            assert [{k:d[k] for k in writes} for d in before]==[{k:d[k] for k in writes} for d in after],(before,after)
        # Wrong capabilities may never reboot.
        try:get("/standby/start","0"*64,True);raise AssertionError("unauthorized restart")
        except urllib.error.HTTPError as e:assert e.code==401
        if args.mode=="magic":
            udp=socket.socket(socket.AF_INET,socket.SOCK_DGRAM)
            mac=bytes.fromhex("525400123456")
            udp.sendto(b"\xff"*6+mac*16,("127.0.0.1",19009))
            udp.close()
        else:get("/standby/start",token,True)
        assert vm.wait(timeout=15)==0,log.read_text(errors="replace")
        if args.filesystem=="ext4":
            writer_log=subprocess.check_output(["debugfs","-R","cat /writer.log",str(disk)],stderr=subprocess.DEVNULL,text=True)
            assert "stopped" in writer_log,writer_log
        assert "reboot: Restarting system" in log.read_text(errors="replace"),log.read_text(errors="replace")
        result={"mode":args.mode,"filesystem":args.filesystem,"status":status,"quiet_seconds":5,"volumes":["root","boot","data"],"mmap_writer":True,"zram":True,"loop_backing_on_data":True,"busy_locks_rejected":True,
                "qmp_before":[{k:d[k] for k in writes} for d in before],"qmp_after":[{k:d[k] for k in writes} for d in after]}
        (q/(args.mode+"-"+args.filesystem+"-result.json")).write_text(json.dumps(result,indent=2)+"\n")
        print(json.dumps(result))
    finally:
        if vm.poll() is None:vm.terminate();vm.wait(timeout=10)
# Boot the same disk again: /run and the temporary inittab must not persist.
with log.open("wb") as stream:
    vm=subprocess.Popen(cmd,stdin=subprocess.DEVNULL,stdout=stream,stderr=subprocess.STDOUT)
    try:
        deadline=time.monotonic()+40
        while time.monotonic()<deadline:
            text=log.read_text(errors="replace")
            if "STANDBY_TEST_ERROR" in text:raise AssertionError(text)
            if "STANDBY_TEST_NORMAL_BOOT" in text:break
            time.sleep(.2)
        else:raise AssertionError(log.read_text(errors="replace"))
        print("PASS: subsequent boot starts normal services without a standby marker")
    finally:
        vm.terminate();vm.wait(timeout=10)
