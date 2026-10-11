# Dependency maintenance

NanoKVM OS uses two complementary daily checks: Dependabot creates dependency PRs, and **Upstream dependency watch** maintains one bot-owned issue with current pins, available versions and check errors. Standard GitHub-hosted runners suffice for this public repository. Neither service deploys to devices or automatically merges changes.

## Dependabot

`.github/dependabot.yml` checks these manifests daily at 06:00 UTC:

- All repository GitHub Actions.
- `web/package.json` and `web/pnpm-lock.yaml`, including development and indirect dependencies.
- All five Go modules, including dependencies inside the patched Pion trees.
- `addons/rustdesk/Cargo.toml` and `Cargo.lock`.
- Both Pion Dockerfiles.

Minor/patch updates are grouped; major npm/Go/Cargo upgrades remain separate candidates. Security updates have separate groups and are enabled in repository settings. The explicit **three-day cooldown** delays normal update PRs, not the dashboard's visibility of new releases. Daily checks avoid adding a full week after that waiting period. Open-PR limits limit queued PRs, not inventory coverage.

Intentional exceptions have separate monitoring:

| Dependabot exception | Independent check / reason |
|---|---|
| Major `@types/node` bumps | Dashboard reports latest; types stay aligned with the selected Node 24 runtime until a deliberate migration. |
| `pion/dtls/v3`, `pion/ice/v4`, `pion/srtp/v3` | Dashboard checks upstream tags. Local replacements contain patches; changing `require` alone would not update their implementation. |
| `github.com/rs/cors/wrapper/gin` | Dashboard checks the Go module proxy's actual nested-module release. Dependabot previously tried a parent tag that contains no such package. |
| `github.com/mervick/aes-everywhere/go/aes256` | Dashboard checks its pinned pseudo-version commit and the current head of the canonical upstream `mervick/aes-bridge` repository (renamed from aes-everywhere). This nested module has no usable proxy `@latest`; parent tags also broke Dependabot. Integration requires manual review, potentially a module-path migration. |

The last two are intentionally handled as manual update candidates, not silently treated as current. Dependabot does not generate updates for ignored module paths, including security updates; these exceptions therefore still need maintainer security review.

The Go, frontend, script and additional-Go-module CI suites remain mandatory. RustDesk now has a host `cargo test --locked` job so Cargo dependency PRs are actually exercised. Existing device-only RustDesk tests stay explicitly ignored without a coordinated device; host CI does not claim hardware qualification.

## Independent daily dashboard

`Upstream dependency watch` runs daily at 05:23 UTC and supports manual dispatch. It reads pins from repository files without executing downloaded package code. It reports:

- **Every entry in `platform/sources.lock`**: Linux, Buildroot, U-Boot, OpenSBI, Sophgo SDK/drivers/media/sensors, AIC8800, cryptodev, MaixCDK, json-c, miniz, inih, tinyalsa, Opus, BusyBox devmem source, wireless-regdb, Go, Node, pnpm, Alpine minirootfs and the QEMU build helper.
- **All direct frontend dependencies**, including dev dependencies and npm aliases, against registry `latest`, independently of cooldown or whether Dependabot can produce a lockfile update. Indirect frontend packages remain owned by Dependabot/security alerts. Unrecognized version ranges fail visibly instead of being skipped.
- **Selected Buildroot recipes** against the pinned release and current Buildroot master: compiler selection (GCC/binutils), musl, initramfs BusyBox, filesystem tools and selected host build tools. GCC stable releases also have their own check. Recipe changes are review candidates, not automatically compatible upgrades.
- **All stock Alpine dependencies named in local APKBUILD `depends`/`makedepends`**, plus optional VPN packages and core BusyBox/OpenSSL/APK tools. Stable main/community availability is checked; optional VPN packages may use the configured edge/community fallback. Installed device versions are not inspected.
- **RustDesk protocol/reference sources**, hbb_common and OneKVM; **PicoClaw's latest-release channel** and presence of the required riscv64 archive.
- Patched Pion versions, the vendor kernel reference, and the two nested Go modules described above.

Errors fail the workflow and remain explicit in the report. A closed dashboard is reopened when actionable results remain, even if the text did not change. Unchanged open reports produce no edits or comments. GitHub tag listing is paginated; exceeding its bounded scan is an error rather than an incomplete claim of the latest version. Pinned-ahead results never trigger a downgrade.

## Which BusyBox and QEMU?

The installed operating system uses **stock Alpine BusyBox** and OpenSSL, updated through signed APK repositories. The optional C906 rebuild scripts are retained for research and are not the production baseline.

There are two separate build inputs: Buildroot's BusyBox for the early-boot initramfs, and the standalone BusyBox source used to build only `devmem` for the vendor media library. They do not replace Alpine's BusyBox.

`qemu-riscv64-static` runs riscv64 Alpine programs and package-install scripts on the x86-64 build host. It is not installed on NanoKVM. The watcher checks published versions in the pinned Ubuntu series; a changed Debian/Ubuntu revision is a review candidate, never a lexicographically selected automatic upgrade.

## Preventing coverage drift

`python3 scripts/check-upstream.py --inventory` validates that:

1. Every tracked npm, Go, Cargo and Docker manifest has a Dependabot directory.
2. Every platform source-lock entry has exactly one monitor owner.
3. Every explicitly selected Buildroot package has a monitored recipe or a declared feature relationship.
4. Source-pin expressions resolve unambiguously; frontend dependency syntax is understood.

The same contracts run in CI. Adding a new input without its monitor fails CI. New Python/Ruby/PHP/Maven manifest families also fail visibly until coverage is implemented.

This is comprehensive coverage of the declared inputs above, **not a claim to discover every upstream vulnerability or unversioned binary**. Transitive native recipes are maintained through Buildroot; the watcher does not independently audit every C library's upstream/CVEs. Opaque base FIP and vendor blobs follow their SDK source pins; local patches require review. Historical experiments that are not active manifests/build inputs are not production dependencies. Additions must be reflected in the coverage contracts.

## Validation and integration

Run `python3 scripts/test-upstream-watch.py` for offline contracts, or `python3 scripts/check-upstream.py --output report.md` for a live read-only report. `--publish` uses a GitHub Actions token with `issues: write`; the default command performs no remote writes.

Dependency availability does not prove compatibility. Native changes need matching hashes, preservation of local patches, rebuilds and relevant validation. Node major changes, Alpine branch migrations, firmware and module ABI changes remain explicit decisions. Actual package installation verifies APK signatures; the dashboard only reads metadata over HTTPS.

References: [Dependabot configuration](https://docs.github.com/en/code-security/reference/supply-chain-security/dependabot-options-reference), [supported ecosystems](https://docs.github.com/en/code-security/reference/supply-chain-security/supported-ecosystems-and-repositories), [default cooldown](https://github.blog/changelog/2026-07-14-dependabot-version-updates-introduce-default-package-cooldown/).

### Audit on 2026-10-12

- ESLint 10.12.0 is visible independently of the three-day PR cooldown (the previous October 5 run occurred before that waiting period elapsed).
- U-Boot 2026.10 is reported from stable tags; the archive index still advertised 2026.07 during this audit.
- The CORS wrapper candidate `v0.0.0-20261008204034-5d834403882d` changes the wrapper's dependency files, not its Go implementation. An isolated alternative go.mod passed server build and middleware/router/auth tests. The production dependency pin is not changed by this monitoring update.
- The AES reference needs manual migration review: the canonical upstream has unrelated main history and no old `go/aes256` directory. It is reported as `review upstream replacement`, not as a normal version bump.
