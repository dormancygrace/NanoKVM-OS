# Current release naming

Starting with **NanoKVM OS v2.5-a1**, application releases and full SD images
are versioned independently. Both `a` releases are alpha, marked as GitHub
prereleases until the maintainer explicitly promotes them.

| Artifact | First release with independent image version |
|---|---|
| Application release / Git tag | `v2.5-a1` |
| GitHub release title | `NanoKVM OS v2.5-a1 · Image v1.0-a1 (Alpha)` |
| Full image | `NanoKVM-OS-Image-v1.0-a1-apps-v2.5-a1.img.zip` |
| APK upstream version | `2.5_alpha1` |
| Corresponding source | `NanoKVM-OS-v2.5-a1-source.tar.xz` |

The full image is an installation bundle. Its version does not replace the
application version or APK ordering. A subsequent application update can be
installed on Image v1.0-a1 without rewriting that image's origin metadata.
The original bundled version stays visible alongside the current application
version in About. A new full image gets an independently selected image version.

Values live in `firmware/alpine/release.env`; the SD builder and build manifest
use them directly. The legacy v1 naming helper remains for archived artifacts.

## Legacy naming (v1 releases)

# Release names

Use `python3 scripts/release_names.py VERSION` as the source of names.
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

## Beta-11 publication

By explicit release instruction, beta-11 uses GitHub tag `v1.0.0-beta.11` and the
Latest release designation. Version/sequence remain `1.0.0-beta.11` / 27.
Only `NanoKVM-OS-v1.0.0-beta-11.img.zip` and `SHA256SUMS` are published;
there is no beta-11 `.nkos` asset.
