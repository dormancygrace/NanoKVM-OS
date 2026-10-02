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
