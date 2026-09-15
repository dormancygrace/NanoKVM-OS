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
| Signed full-system package | `NanoKVM-OS-update.nkos` | `NanoKVM-OS-v1.0.0-beta-11.nkos` | `NanoKVM-OS-v1.0.0.nkos` |

The ZIP contains the same stem with `.img`. Checksums use `SHA256SUMS`.
Internal sequence numbers and build qualifiers do not appear in published filenames.
The updater accepts these versioned package names, binds each to its release tag,
and retains support for the two historical fixed package names.

Beta-10 is the final compatibility exception: retain the old image name and fixed
`NanoKVM-OS-update.nkos` name so beta-9 discovers it. Beta-10 adds versioned
package discovery, allowing beta-11 and later to use the canonical names above.

## Beta-11 publication

By explicit release instruction, beta-11 uses GitHub tag `v1.0.0-beta.11` and the
Latest release designation. Version/sequence remain `1.0.0-beta.11` / 27.
Only `NanoKVM-OS-v1.0.0-beta-11.img.zip` and `SHA256SUMS` are published;
there is no beta-11 `.nkos` asset.
