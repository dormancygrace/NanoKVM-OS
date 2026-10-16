# Network boot (netboot.xyz)

Network boot starts the host from the [netboot.xyz](https://netboot.xyz) menus:
operating system installers, live systems and tools load from the internet
through the NanoKVM. Only the iPXE image (a few MiB) is stored on the NanoKVM.

Images → Network boot in the web UI turns it on and off.

## How it works

1. The official iPXE CD image goes on the virtual disk as a read-only CD/DVD
   drive, exactly as the iPXE project publishes it.
2. The host boots from that drive. The image carries no script of its own, so
   iPXE runs `autoboot`: it asks DHCP on the USB network for a boot file.
3. The NanoKVM DHCP server on usb0 answers iPXE, and only iPXE (DHCP option
   175), with `http://<usb0 address>:8070/boot.ipxe`. The operating system
   booted afterwards never sees a boot file.
4. That script sets what the netboot.xyz Secure Boot `autoexec.ipxe` of the
   selected release sets and loads `https://boot.netboot.xyz/<release>/menu.ipxe`
   (falling back to plain HTTP).
5. Everything after that (the menus, kernels, installers) comes from the
   internet through USB internet sharing.

The script listener binds the usb0 address only, serves nothing but
`/boot.ipxe`, and follows a subnet change of the USB network.

## Requirements

- USB network and the virtual disk in the USB composition (the KVM + USB
  network preset has both).
- An internet connection on the NanoKVM (Ethernet or Wi-Fi). Network boot turns
  on [USB internet sharing](usb-internet-sharing.md) while it is on and turns it
  off again afterwards if it was off before. While sharing is on, the host gets
  its default route and DNS from the NanoKVM.
- The host must boot from the NanoKVM CD/DVD drive (boot menu or boot order).

Network boot replaces the mounted image. Image mounts and browser media are
refused while it is on.

## Releases and updates

Network boot uses the latest stable releases:

| Component | Source | Stored on the NanoKVM |
| --- | --- | --- |
| iPXE | `github.com/ipxe/ipxe` latest release: `ipxe.iso` and `ipxe-x86_64-sb.iso` | `/data/.nanokvm/netboot/ipxe-<tag>/` |
| netboot.xyz | `github.com/netbootxyz/netboot.xyz` latest release; menus from `boot.netboot.xyz/<tag>/` | the release tag only |

The first time network boot is turned on it downloads both images (about
13 MB). **Check for updates** installs newer releases. Every iPXE image must
match the SHA-256 digest GitHub publishes for it; a mismatching or partial
download never replaces the installed image, and the image is checked again
before it is mounted. A netboot.xyz release is selected only when
boot.netboot.xyz serves its menus.

State lives in `/data/.nanokvm/netboot/state.json`.

## Secure Boot

With **UEFI Secure Boot** selected, the NanoKVM mounts `ipxe-x86_64-sb.iso`: a
shim signed with the Microsoft third-party UEFI CA that starts the iPXE binary
signed by the iPXE Secure Boot CA. It works on x86-64 UEFI machines that trust
the Microsoft third-party CA (the default on most PCs). Without it,
`ipxe.iso` covers BIOS and UEFI on every architecture iPXE supports.

What boots under Secure Boot is up to each netboot.xyz entry. netboot.xyz
installs Debian, Devuan and Kali end to end (through their own shims); other
entries load kernels that the firmware rejects ("Security Policy Violation")
and need Secure Boot turned off in the host firmware. See
[netboot.xyz: UEFI Secure Boot](https://netboot.xyz/docs/kb/hardware/secureboot/).

Memtest86+ 9.00 is signed with its own CA: it boots under Secure Boot once the
[Memtest86+ root CA](https://github.com/memtest86plus/memtest86plus/tree/main/secureboot)
is added to the firmware `db`, from the netboot.xyz release whose menus offer
9.00.

## Lifetime

Network boot is a runtime state, like an image mount. A NanoKVM restart ends
it; the next server start ejects the iPXE image, withdraws the boot file from
DHCP and restores USB internet sharing to the user's setting.

## Speed

Downloads from the internet run at the speed of the NanoKVM's own connection.
Measured through a Wi-Fi uplink at 30 Mbit/s: 1.86 MB/s through the NanoKVM,
1.96 MB/s on the NanoKVM itself. The USB link itself carried 18-19 MB/s.

## API

| Method | Path | Body |
| --- | --- | --- |
| GET | `/api/storage/netboot` | status: versions, enabled, Secure Boot, menu URL and fetches, composition, sharing |
| POST | `/api/storage/netboot/update` | download the latest releases |
| POST | `/api/storage/netboot/enable` | `{"secureBoot": true}` |
| POST | `/api/storage/netboot/disable` | |

All routes require the admin role.
