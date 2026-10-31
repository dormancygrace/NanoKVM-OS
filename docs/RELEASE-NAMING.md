# Current release naming

New full SD images share the NanoKVM OS release version. There is no separate
image counter. The release workflow selects one OS version; the image builder
uses it for the filename and original-installation metadata.

| Artifact | Stable example | Beta example |
|---|---|---|
| OS release / Git tag | `v3.7.0` | `v3.7.0-beta.2` |
| GitHub release title | `NanoKVM OS v3.7.0` | `NanoKVM OS v3.7.0-beta.2` |
| Full SD image | `NanoKVM-OS-v3.7.0.img.zip` | `NanoKVM-OS-v3.7.0-beta.2.img.zip` |
| APK upstream version | `3.7.0` | `3.7.0_beta2` |
| Corresponding source | `NanoKVM-OS-v3.7.0-source.tar.xz` | `NanoKVM-OS-v3.7.0-beta.2-source.tar.xz` |

The ZIP contains the same stem with `.img`. Checksums use `SHA256SUMS`.
A local debug image adds `-debug` before `.img.zip`; this is a build-profile
qualifier, not an independent release version.

A patch such as `v3.7.1` may publish only packages and corresponding source.
The latest-image badge and download link then remain on the newest release
that actually includes an image. Stable excludes beta images; the beta channel
can offer either stable or beta releases according to its existing ordering.

`firmware/alpine/release.env` defines the OS baseline and its APK-comparable
version. `/boot/image.json` records the original installation and is not replaced
by package updates. About can therefore show OS `v3.7.1` installed from image
`v3.7.0` without implying that the SD card was reflashed.

## Historical independent image versions

Releases from applications **v2.5-a1** used a separate Image v1.x series, such as
`NanoKVM-OS-Image-v1.0-a1-apps-v2.5-a1.img.zip` and
`NanoKVM-OS-Image-v1.8-apps-v3.6.0.img.zip`. Those published assets and installed
metadata keep their original names. Pages and publication recovery recognize
both formats; About retains the bundled application version when it differs
from the historical image number.

## Legacy naming (v1 releases)

The OS version and Git tag retain SemVer. Starting with beta-11, release titles and filenames share
the `NanoKVM-OS-v` prefix and use a hyphen before the prerelease number.

| Item | beta-10 | Next beta | Stable |
| --- | --- | --- | --- |
| OS version | `1.0.0-beta.10` | `1.0.0-beta.11` | `1.0.0` |
| Git tag | `v1.0.0-beta.10` | `v1.0.0-beta.11` | `v1.0.0` |
| Release title | `NanoKVM-OS-v1.0.0-beta-10` | `NanoKVM-OS-v1.0.0-beta-11` | `NanoKVM-OS-v1.0.0` |
| Full SD image ZIP | `NanoKVM-OS-1.0.0-beta.10.img.zip` | `NanoKVM-OS-v1.0.0-beta-11.img.zip` | `NanoKVM-OS-v1.0.0.img.zip` |

The ZIP contains the same stem with `.img`. Checksums use `SHA256SUMS`.
Internal sequence numbers and build qualifiers do not appear in published filenames.

Beta-10 is the final compatibility exception: it retains the old image name so beta-9
discovers it. The legacy `.nkos` updater has since been removed; NanoKVM OS v2 updates
through native APK packages.
