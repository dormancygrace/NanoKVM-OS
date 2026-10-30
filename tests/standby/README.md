# Isolated standby integration test

Run on Linux x86-64. `qemu.py` boots a disposable VM; never run `nkos-standby
takeover` or `shutdown` against the host init. No NanoKVM device is contacted.

Dependencies: Go matching the server module, Python 3, qemu-system-x86_64,
BusyBox **static**, cpio, gzip, squashfs-tools, e2fsprogs, f2fs-tools
(`mkfs.f2fs` and `sload.f2fs`), dosfstools, exfatprogs and mtools. The fixture
uses localhost TCP 18081, UDP 19009 and 256 MiB guest RAM. Run cases sequentially.

Set `OUT` to a disposable task build directory, and create `$OUT/qemu`.
Download these Alpine x86-64 inputs there:

| File | URL suffix under https://dl-cdn.alpinelinux.org/alpine/v3.23/releases/x86_64/ | SHA-256 used |
| --- | --- | --- |
| minirootfs.tar.gz | alpine-minirootfs-3.23.0-x86_64.tar.gz | ce8f782f1628d046fb6360eff880b898e5205ed91106d9d14ff4fcb97431bbde |
| vmlinuz-virt | netboot/vmlinuz-virt | 456e0691f095285a82533cc6335280e4b4991d630c5292ddf15e6320e71971c9 |
| initramfs-virt | netboot/initramfs-virt | 7b4c6a959ee6c3cbd81c98f7dc3f991790b03b085af3d3efd20b5fb72e0e10dd |
| modloop-virt | netboot/modloop-virt | 237f5e31e95071816565ffcc55bc028cbdeed960e8e54efd134c8cf13b101782 |

The netboot URLs are rolling. Verify hashes and retain a matching
kernel/initramfs/modloop set; do not silently substitute just one file.
The measured set is Linux 6.18.58-0-virt, OpenRC 0.63-r1, BusyBox 1.37.0-r29,
busybox-openrc 1.37.0-r30. This is a host mechanism test, not the board kernel.

Prepare empty `root` and `initrd` directories:

```sh
mkdir -p "$OUT/qemu/root" "$OUT/qemu/initrd"
tar --no-same-owner -xzf "$OUT/qemu/minirootfs.tar.gz" -C "$OUT/qemu/root"
cp /etc/resolv.conf "$OUT/qemu/root/etc/resolv.conf"
mkdir -p "$OUT/qemu/root/etc/ssl/certs"
cp /etc/ssl/certs/ca-certificates.crt "$OUT/qemu/root/etc/ssl/certs/"
sudo chroot "$OUT/qemu/root" /sbin/apk add --no-cache openrc busybox-openrc
(cd "$OUT/qemu/initrd" && gzip -dc ../initramfs-virt | cpio -id --quiet)
unsquashfs -f -d "$OUT/qemu/initrd/lib" "$OUT/qemu/modloop-virt"
cp /usr/bin/busybox "$OUT/qemu/initrd/bin/busybox"
for name in sh mount mkdir ln seq sleep switch_root modprobe; do
    ln -sf busybox "$OUT/qemu/initrd/bin/$name"
done
sudo chown -R "$(id -u):$(id -g)" "$OUT/qemu/root" "$OUT/qemu/initrd"
```

Verify `/usr/bin/busybox` is static before copying it. Privilege is needed only
to install the disposable chroot packages and restore ownership; the VM and
all tests run as the ordinary user. No guest filesystems are mounted on the
host. Record `root/lib/apk/db/installed` if package versions have advanced.

From the repository root:

```sh
for mode in web dhcp magic held-mount hidden-mount stop-error; do
    python3 tests/standby/qemu.py "$OUT" --filesystem f2fs --mode "$mode"
done
```

F2FS is the default and primary matrix. `--filesystem ext4` is an additional
compatibility check. Each case recreates regular-file disk images, builds the
production helper and a test-only driver, checks active-lock refusal, dirty
mmap and writers on all volumes plus a loop disk backed by `/data`, enables zram swap, and runs the real init
handoff. QMP counters must remain unchanged while ready. Fault cases must fail
closed, recover by authenticated reboot, and boot normally next time with all
final writer/mmap data intact.

Results are `qemu/<mode>-<filesystem>-result.json`. Console output is overwritten
between boots; it contains only a disposable guest capability, never a device
credential. The fixture root, initrd and disk images are temporary test
artifacts and can be removed after retaining compact results.
