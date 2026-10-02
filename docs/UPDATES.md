# NanoKVM OS v2 package updates

NanoKVM OS v2 uses native APK packages in the writable system root. In the web
interface, use Settings -> System -> Updates (Package updates; on a2,
Settings -> Updates). On the command line use
`apk update` and `apk upgrade`; install optional software with `apk add NAME`
and remove it with `apk del NAME`. APK resolves dependencies and verifies
repository signatures.

A completed package transaction refreshes affected services through OpenRC.
The app may briefly disconnect video/control; no separate apply command is needed.
Kernel packages require matching modules, activate the detected board boot image
and request a device restart. Ordinary updates preserve the existing rootfs and
NanoKVM settings. Full reinstallation is a separate, destructive rootfs operation.

Coming from beta-14 or an older beta requires the full v2 SD image; legacy
`.nkos` packages do not perform this migration. The original preserved a1 image
needs preparatory application/base updates for the native GUI controls. The a2
image includes these controls from initial installation.

## Updating v2.0-a2 or later to b7

Use the existing GUI package updater, or `apk update` followed by `apk upgrade`,
then reboot. No reflash or new signing key is needed. Settings and independently
installed packages are retained; Linux remains 7.2.6-nanokvm-os-r1. Coming from
a2, see RELEASE-v2.0-b1.md for the optional OpenVPN 2 migration and the Software
settings; see RELEASE-v2.0-b7.md for the current release.

b5 was also distributed as separate APK files. Installing local `.apk` files
pins those packages in `/etc/apk/world` to the exact files, so `apk upgrade`
keeps them at b5. Release the pin first:

```sh
apk add nanokvm-base nanokvm-app nanokvm-release
apk upgrade
```
