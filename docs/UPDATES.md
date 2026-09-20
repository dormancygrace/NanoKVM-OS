# NanoKVM OS v2 package updates

NanoKVM OS v2 uses native APK packages in the writable system root. In the web
interface, use Settings -> Updates -> Package updates. On the command line use
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

## Updating a2 to b1

Use the existing GUI package updater, or `apk update` followed by `apk upgrade`.
No reflash or new signing key is needed. Settings and independently installed
packages are retained; Linux remains 7.2.6-nanokvm-os-r1. The three NanoKVM
component packages total 27.2 MB. See RELEASE-v2.0-b1.md for the optional
OpenVPN 2 migration and new Software settings.
