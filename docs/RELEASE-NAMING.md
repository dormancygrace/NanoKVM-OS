# Release naming

New full SD images share the NanoKVM OS release version. There is no separate
image counter. The release workflow selects one OS version; the image builder
uses it for the filename and original-installation metadata.

| Artifact | Stable example | Beta example |
|---|---|---|
| OS release / Git tag | `v4.0.0` | `v4.0.0-beta.2` |
| GitHub release title | `NanoKVM OS v4.0.0` | `NanoKVM OS v4.0.0-beta.2` |
| Full SD image | `NanoKVM-OS-v4.0.0.img.zip` | `NanoKVM-OS-v4.0.0-beta.2.img.zip` |
| APK upstream version | `4.0.0` | `4.0.0_beta2` |
| Corresponding source | `NanoKVM-OS-v4.0.0-source.tar.xz` | `NanoKVM-OS-v4.0.0-beta.2-source.tar.xz` |
| Stock migration package | `nanokvm_4.0.0.tar.gz` | `nanokvm_4.0.0.tar.gz` (for `v4.0.0-beta.2`) |

The ZIP contains the same stem with `.img`. Checksums use `SHA256SUMS`.

Releases that include an SD image also include the
[stock migration package](STOCK-INSTALL.md). Its numeric `nanokvm_X.Y.Z.tar.gz`
filename is required by the Sipeed offline updater; do not rename it. The
archive records the complete target version internally, including a beta
suffix. It is a public download alongside the image and corresponding source;
APK packages and indexes remain in technical package storage.
A local debug image adds `-debug` before `.img.zip`; this is a build-profile
qualifier, not an independent release version.

A patch such as `v4.0.1` may publish only packages and corresponding source.
Select **packages_only** in the Release workflow for an OTA patch without a
GitHub Release, SD image or stock installer. The signed package snapshot and
corresponding source are published through Pages; a Git tag reserves the version.
Pages keeps three stable snapshots and one beta snapshot, including these patches.
Devices discover them through the same signed APK indexes, and beta↔stable
switching uses the same package feeds. Source and checksums are available under
updates/vX.Y.Z/ on Pages. Subsequent version selection includes Pages updates
and reserved tags.

The latest-image badge and download link then remain on the newest release
that actually includes an image. Stable excludes beta images; the beta channel
can offer either stable or beta releases according to its existing ordering.

`firmware/alpine/release.env` defines the OS baseline and its APK-comparable
version. `/boot/image.json` records the original installation and is not replaced
by package updates. About can therefore show OS `v4.0.1` installed from image
`v4.0.0` without implying that the SD card was reflashed.

## Separate image versions

Images with a separate image number, such as
`NanoKVM-OS-Image-v1.8-apps-v3.6.0.img.zip`, keep that name in their published
assets and installed metadata. Pages and publication recovery recognize both
formats; About shows the bundled application version when it differs from the
image number.
